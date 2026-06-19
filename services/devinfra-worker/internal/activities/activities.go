// Package activities реализует Temporal-activities провизии и компенсаций,
// исполняемые DevInfra worker'ом. Каждая activity делегирует клиенту интеграции
// (за интерфейсом) и регистрируется под именем из пакета provisioning. Фатальные
// (неустранимые ретраем) ошибки оборачиваются в non-retryable ApplicationError,
// чтобы workflow ушёл в ветку компенсации (ADR-0005).
package activities

import (
	"context"
	"errors"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"

	"github.com/YuriB9/idp/pkg/errs"
	"github.com/YuriB9/idp/services/devinfra-worker/internal/integrations"
	"github.com/YuriB9/idp/services/projects/changeowners"
	"github.com/YuriB9/idp/services/projects/provisioning"
)

// StatusStore — зависимость финальных activities от слоя каталога (guarded-CAS).
type StatusStore interface {
	Activate(ctx context.Context, serviceID string) error
	Fail(ctx context.Context, serviceID string) error
}

// OwnersStore — зависимость activity записи владельцев от слоя каталога
// (guarded-CAS по версии).
type OwnersStore interface {
	SetOwners(ctx context.Context, serviceID string, desired []string, expectedVersion int64) error
}

// RoleAdmin — зависимость activity синхронизации ролей от IDM (выдача/отзыв роли;
// инвалидация кэша выполняется самим IDM).
type RoleAdmin interface {
	AssignRole(ctx context.Context, subject, role string) error
	RevokeRole(ctx context.Context, subject, role string) error
}

// Activities связывает клиентов интеграций и слой каталога с activity-методами.
type Activities struct {
	gitlab   integrations.GitLab
	harbor   integrations.Harbor
	vault    integrations.Vault
	status   StatusStore
	owners   OwnersStore
	idmRoles RoleAdmin
}

// Option конфигурирует Activities (для сценариев сверх провижна).
type Option func(*Activities)

// WithOwners подключает запись владельцев в каталог (сценарий смены владельцев).
func WithOwners(o OwnersStore) Option { return func(a *Activities) { a.owners = o } }

// WithIDMRoles подключает синхронизацию ролей IDM (сценарий смены владельцев).
func WithIDMRoles(r RoleAdmin) Option { return func(a *Activities) { a.idmRoles = r } }

// New собирает набор activities. Зависимости сценария смены владельцев
// (OwnersStore/RoleAdmin) подключаются опциями WithOwners/WithIDMRoles.
func New(clients *integrations.Clients, status StatusStore, opts ...Option) *Activities {
	a := &Activities{gitlab: clients.GitLab, harbor: clients.Harbor, vault: clients.Vault, status: status}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// fatalType — общий тип non-retryable ошибки для ветки компенсации.
const fatalType = "ProvisioningFatal"

// classify оборачивает заведомо неустранимые ретраем ошибки (валидация, доступ,
// конфликт guarded-CAS) в non-retryable ApplicationError. Прочие (сетевые/5xx)
// остаются retryable и повторяются по RetryPolicy.
func classify(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, errs.ErrValidation),
		errors.Is(err, errs.ErrUnauthorized),
		errors.Is(err, errs.ErrForbidden),
		errors.Is(err, errs.ErrConflict):
		return temporal.NewNonRetryableApplicationError(err.Error(), fatalType, err)
	default:
		return err
	}
}

// --- GitLab ---

// GitLabCreateRepo создаёт репозиторий в группе проекта.
func (a *Activities) GitLabCreateRepo(ctx context.Context, ref provisioning.ResourceRef) (provisioning.GitLabRepo, error) {
	activity.RecordHeartbeat(ctx)
	repo, err := a.gitlab.CreateRepo(ctx, ref)
	return repo, classify(err)
}

// GitLabDeleteRepo — компенсация: удаляет репозиторий (идемпотентно).
func (a *Activities) GitLabDeleteRepo(ctx context.Context, ref provisioning.ResourceRef) error {
	activity.RecordHeartbeat(ctx)
	return a.gitlab.DeleteRepo(ctx, ref)
}

// GitLabInjectSecrets записывает секреты Vault/Harbor в CI/CD-переменные GitLab.
func (a *Activities) GitLabInjectSecrets(ctx context.Context, in provisioning.InjectSecretsInput) error {
	activity.RecordHeartbeat(ctx)
	return classify(a.gitlab.InjectVariables(ctx, in))
}

// --- Harbor ---

// HarborCreateProject создаёт директорию образов и Robot Account.
func (a *Activities) HarborCreateProject(ctx context.Context, ref provisioning.ResourceRef) (provisioning.HarborResult, error) {
	activity.RecordHeartbeat(ctx)
	res, err := a.harbor.CreateProject(ctx, ref)
	return res, classify(err)
}

// HarborDeleteProject — компенсация: удаляет директорию образов (идемпотентно).
func (a *Activities) HarborDeleteProject(ctx context.Context, ref provisioning.ResourceRef) error {
	activity.RecordHeartbeat(ctx)
	return a.harbor.DeleteProject(ctx, ref)
}

// --- Vault ---

// VaultSetupAppRole создаёт политики и AppRole, возвращает RoleID/SecretID.
func (a *Activities) VaultSetupAppRole(ctx context.Context, ref provisioning.ResourceRef) (provisioning.VaultResult, error) {
	activity.RecordHeartbeat(ctx)
	res, err := a.vault.SetupAppRole(ctx, ref)
	return res, classify(err)
}

// VaultTeardownAppRole — компенсация: удаляет политики и AppRole (идемпотентно).
func (a *Activities) VaultTeardownAppRole(ctx context.Context, ref provisioning.ResourceRef) error {
	activity.RecordHeartbeat(ctx)
	return a.vault.TeardownAppRole(ctx, ref)
}

// --- Каталог (финальные переходы статуса) ---

// CatalogTransitionActive переводит запись CREATING→ACTIVE (guarded-CAS).
func (a *Activities) CatalogTransitionActive(ctx context.Context, ref provisioning.ResourceRef) error {
	// Проигранный guarded-CAS (ErrConflict) ретраем не исправить → non-retryable.
	return classify(a.status.Activate(ctx, ref.ServiceID))
}

// CatalogTransitionFailed переводит запись CREATING→FAILED (guarded-CAS).
func (a *Activities) CatalogTransitionFailed(ctx context.Context, ref provisioning.ResourceRef) error {
	return classify(a.status.Fail(ctx, ref.ServiceID))
}

// --- Изменение владельцев ---

// GitLabSyncMembers синхронизирует участников репозитория по diff (add/remove).
func (a *Activities) GitLabSyncMembers(ctx context.Context, in changeowners.SyncMembersInput) error {
	activity.RecordHeartbeat(ctx)
	return classify(a.gitlab.SyncMembers(ctx, in.Ref, in.Add, in.Remove))
}

// GitLabRestoreMembers — компенсация: восстанавливает прежний состав участников.
func (a *Activities) GitLabRestoreMembers(ctx context.Context, in changeowners.RestoreInput) error {
	activity.RecordHeartbeat(ctx)
	return a.gitlab.RestoreMembers(ctx, in.Ref, in.Previous)
}

// VaultSyncOwners синхронизирует политики доступа владельцев по diff.
func (a *Activities) VaultSyncOwners(ctx context.Context, in changeowners.SyncMembersInput) error {
	activity.RecordHeartbeat(ctx)
	return classify(a.vault.SyncOwners(ctx, in.Ref, in.Add, in.Remove))
}

// VaultRestoreOwners — компенсация: восстанавливает прежние политики доступа.
func (a *Activities) VaultRestoreOwners(ctx context.Context, in changeowners.RestoreInput) error {
	activity.RecordHeartbeat(ctx)
	return a.vault.RestoreOwners(ctx, in.Ref, in.Previous)
}

// CatalogSetOwners — guarded-CAS запись набора владельцев в каталог (точка
// невозврата). Конфликт версии (ErrConflict) ретраем не исправить → non-retryable.
func (a *Activities) CatalogSetOwners(ctx context.Context, in changeowners.CatalogSetOwnersInput) error {
	return classify(a.owners.SetOwners(ctx, in.ServiceID, in.Desired, in.ExpectedVersion))
}

// IDMSyncOwnerRoles выдаёт роль владельца добавленным субъектам и отзывает у
// удалённых через IDM (инвалидация кэша — на стороне IDM). Идемпотентно.
func (a *Activities) IDMSyncOwnerRoles(ctx context.Context, in changeowners.IDMSyncInput) error {
	activity.RecordHeartbeat(ctx)
	role := "owner:project:" + in.Project
	for _, subject := range in.Add {
		if err := a.idmRoles.AssignRole(ctx, subject, role); err != nil {
			return classify(err)
		}
	}
	for _, subject := range in.Remove {
		if err := a.idmRoles.RevokeRole(ctx, subject, role); err != nil {
			return classify(err)
		}
	}
	return nil
}

// Register регистрирует все activities под именами из пакетов provisioning и
// changeowners на переданном worker'е.
func (a *Activities) Register(r registrar) {
	reg := func(fn any, name string) {
		r.RegisterActivityWithOptions(fn, activityOptions(name))
	}
	reg(a.GitLabCreateRepo, provisioning.ActivityGitLabCreateRepo)
	reg(a.GitLabDeleteRepo, provisioning.ActivityGitLabDeleteRepo)
	reg(a.HarborCreateProject, provisioning.ActivityHarborCreate)
	reg(a.HarborDeleteProject, provisioning.ActivityHarborDelete)
	reg(a.VaultSetupAppRole, provisioning.ActivityVaultSetup)
	reg(a.VaultTeardownAppRole, provisioning.ActivityVaultTeardown)
	reg(a.GitLabInjectSecrets, provisioning.ActivityInjectSecrets)
	reg(a.CatalogTransitionActive, provisioning.ActivityTransitionActive)
	reg(a.CatalogTransitionFailed, provisioning.ActivityTransitionFailed)
	// Изменение владельцев.
	reg(a.GitLabSyncMembers, changeowners.ActivityGitLabSyncMembers)
	reg(a.GitLabRestoreMembers, changeowners.ActivityGitLabRestoreMembers)
	reg(a.VaultSyncOwners, changeowners.ActivityVaultSyncOwners)
	reg(a.VaultRestoreOwners, changeowners.ActivityVaultRestoreOwners)
	reg(a.CatalogSetOwners, changeowners.ActivityCatalogSetOwners)
	reg(a.IDMSyncOwnerRoles, changeowners.ActivityIDMSyncOwnerRoles)
}
