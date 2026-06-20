// Package activities реализует Temporal-activities провизии и компенсаций,
// исполняемые DevInfra worker'ом. Каждая activity делегирует клиенту интеграции
// (за интерфейсом) и регистрируется под именем из пакета provisioning. Фатальные
// (неустранимые ретраем) ошибки оборачиваются в non-retryable ApplicationError,
// чтобы workflow ушёл в ветку компенсации (ADR-0005).
package activities

import (
	"context"
	"errors"
	"fmt"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"

	"github.com/YuriB9/idp/pkg/errs"
	"github.com/YuriB9/idp/services/devinfra-worker/internal/integrations"
	"github.com/YuriB9/idp/services/projects/changeowners"
	"github.com/YuriB9/idp/services/projects/decommission"
	"github.com/YuriB9/idp/services/projects/provisioning"
	"github.com/YuriB9/idp/services/projects/transfer"
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

// DecommStore — зависимость activity вывода из эксплуatации от слоя каталога
// (guarded-CAS ACTIVE→DECOMMISSIONED, ADR-0012).
type DecommStore interface {
	Decommission(ctx context.Context, serviceID string) error
}

// TransferStore — зависимость activities переноса от слоя каталога (guarded-CAS
// смены project в две фазы + компенсация, ADR-0013).
type TransferStore interface {
	BeginTransfer(ctx context.Context, serviceID, target string) error
	CommitTransfer(ctx context.Context, serviceID, target string) error
	AbortTransfer(ctx context.Context, serviceID string) error
}

// LoadChecker — граница проверки снятой нагрузки K8s (ADR-0012). В MVP реализация
// опирается на явный флаг load_drained; будущий K8s-worker подменит её реальным
// запросом к кластеру без изменения тела workflow.
type LoadChecker interface {
	EnsureDrained(ctx context.Context, ref provisioning.ResourceRef, loadDrained bool) error
}

// FlagLoadChecker — MVP-реализация LoadChecker: трактует переданный флаг
// load_drained как предусловие (K8s-worker в MVP отсутствует). Невыполненное
// предусловие → errs.ErrPrecondition (далее non-retryable, ADR-0012).
type FlagLoadChecker struct{}

// EnsureDrained проверяет явное предусловие снятой нагрузки.
func (FlagLoadChecker) EnsureDrained(_ context.Context, _ provisioning.ResourceRef, loadDrained bool) error {
	if !loadDrained {
		return fmt.Errorf("activities: нагрузка не снята из K8s: %w", errs.ErrPrecondition)
	}
	return nil
}

// Activities связывает клиентов интеграций и слой каталога с activity-методами.
type Activities struct {
	gitlab      integrations.GitLab
	harbor      integrations.Harbor
	vault       integrations.Vault
	status      StatusStore
	owners      OwnersStore
	idmRoles    RoleAdmin
	decomm      DecommStore
	xfer        TransferStore
	loadChecker LoadChecker
}

// Option конфигурирует Activities (для сценариев сверх провижна).
type Option func(*Activities)

// WithOwners подключает запись владельцев в каталог (сценарий смены владельцев).
func WithOwners(o OwnersStore) Option { return func(a *Activities) { a.owners = o } }

// WithIDMRoles подключает синхронизацию ролей IDM (сценарий смены владельцев).
func WithIDMRoles(r RoleAdmin) Option { return func(a *Activities) { a.idmRoles = r } }

// WithDecommission подключает перевод каталога в DECOMMISSIONED (сценарий вывода
// из эксплуатации).
func WithDecommission(d DecommStore) Option { return func(a *Activities) { a.decomm = d } }

// WithLoadChecker подменяет проверку снятой нагрузки K8s (по умолчанию —
// FlagLoadChecker). Точка расширения под будущий K8s-worker (ADR-0012).
func WithLoadChecker(l LoadChecker) Option { return func(a *Activities) { a.loadChecker = l } }

// WithTransfer подключает смену project в каталоге (сценарий переноса, ADR-0013).
func WithTransfer(t TransferStore) Option { return func(a *Activities) { a.xfer = t } }

// New собирает набор activities. Зависимости сценариев смены владельцев и вывода
// из эксплуатации подключаются опциями WithOwners/WithIDMRoles/WithDecommission.
// Проверка снятой нагрузки по умолчанию — FlagLoadChecker (MVP, ADR-0012).
func New(clients *integrations.Clients, status StatusStore, opts ...Option) *Activities {
	a := &Activities{gitlab: clients.GitLab, harbor: clients.Harbor, vault: clients.Vault, status: status, loadChecker: FlagLoadChecker{}}
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
		errors.Is(err, errs.ErrConflict),
		errors.Is(err, errs.ErrPrecondition):
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

// --- Вывод из эксплуатации (soft-delete) ---

// EnsureLoadDrained проверяет предусловие снятой нагрузки K8s до любых отзывов
// доступа (ADR-0012). Невыполнение → non-retryable ErrPrecondition.
func (a *Activities) EnsureLoadDrained(ctx context.Context, in decommission.EnsureLoadDrainedInput) error {
	activity.RecordHeartbeat(ctx)
	return classify(a.loadChecker.EnsureDrained(ctx, in.Ref, in.LoadDrained))
}

// GitLabArchive архивирует репозиторий и отзывает доступы участников (идемпотентно).
func (a *Activities) GitLabArchive(ctx context.Context, ref provisioning.ResourceRef) error {
	activity.RecordHeartbeat(ctx)
	return classify(a.gitlab.Archive(ctx, ref))
}

// GitLabUnarchive — компенсация: разархивирует репозиторий (идемпотентно).
func (a *Activities) GitLabUnarchive(ctx context.Context, ref provisioning.ResourceRef) error {
	activity.RecordHeartbeat(ctx)
	return a.gitlab.Unarchive(ctx, ref)
}

// HarborSetReadOnly переводит директорию образов в read-only и отзывает Robot.
func (a *Activities) HarborSetReadOnly(ctx context.Context, ref provisioning.ResourceRef) error {
	activity.RecordHeartbeat(ctx)
	return classify(a.harbor.SetReadOnly(ctx, ref))
}

// HarborSetWritable — компенсация: возвращает директорию в writable (идемпотентно).
func (a *Activities) HarborSetWritable(ctx context.Context, ref provisioning.ResourceRef) error {
	activity.RecordHeartbeat(ctx)
	return a.harbor.SetWritable(ctx, ref)
}

// VaultRevokeSecretID отзывает активные SecretID/токены сервиса — немедленное
// прекращение доступа (идемпотентно). Точка невозврата (необратимо), компенсации нет.
func (a *Activities) VaultRevokeSecretID(ctx context.Context, ref provisioning.ResourceRef) error {
	activity.RecordHeartbeat(ctx)
	return classify(a.vault.RevokeSecretID(ctx, ref))
}

// CatalogDecommission — guarded-CAS перевод ACTIVE→DECOMMISSIONED (идемпотентно).
// Конфликт версии/статуса (ErrConflict) ретраем не исправить → non-retryable.
func (a *Activities) CatalogDecommission(ctx context.Context, ref provisioning.ResourceRef) error {
	return classify(a.decomm.Decommission(ctx, ref.ServiceID))
}

// --- Перенос сервиса (смена project) ---

// CatalogBeginTransfer — guarded-CAS ACTIVE→TRANSFERRING с проверкой свободы
// (target, name) (идемпотентно). Занятое имя/конфликт/предусловие (ErrConflict/
// ErrPrecondition) ретраем не исправить → non-retryable.
func (a *Activities) CatalogBeginTransfer(ctx context.Context, in transfer.CatalogTransferInput) error {
	return classify(a.xfer.BeginTransfer(ctx, in.ServiceID, in.Target))
}

// CatalogCommitTransfer — guarded-CAS TRANSFERRING→ACTIVE + project=target
// (идемпотентно). Конфликт guarded-CAS (ErrConflict) → non-retryable.
func (a *Activities) CatalogCommitTransfer(ctx context.Context, in transfer.CatalogTransferInput) error {
	return classify(a.xfer.CommitTransfer(ctx, in.ServiceID, in.Target))
}

// CatalogAbortTransfer — компенсация: guarded-CAS TRANSFERRING→ACTIVE (идемпотентно).
func (a *Activities) CatalogAbortTransfer(ctx context.Context, in transfer.CatalogTransferInput) error {
	return a.xfer.AbortTransfer(ctx, in.ServiceID)
}

// GitLabTransferRepo переносит репозиторий в группу target (идемпотентно). Точка
// невозврата: компенсации нет.
func (a *Activities) GitLabTransferRepo(ctx context.Context, ref transfer.TransferRef) error {
	activity.RecordHeartbeat(ctx)
	r := provisioning.ResourceRef{ServiceID: ref.ServiceID, Project: ref.Source, Name: ref.Name}
	return classify(a.gitlab.TransferRepo(ctx, r, ref.Target))
}

// VaultMigratePaths мигрирует пути секретов source→target (копия + новые политики
// + очистка старых, идемпотентно). Секреты не логируются.
func (a *Activities) VaultMigratePaths(ctx context.Context, ref transfer.TransferRef) error {
	activity.RecordHeartbeat(ctx)
	r := provisioning.ResourceRef{ServiceID: ref.ServiceID, Project: ref.Source, Name: ref.Name}
	return classify(a.vault.MigratePaths(ctx, r, ref.Target))
}

// HarborUpdateMetadata обновляет метаданные/права директории под target (идемпотентно).
func (a *Activities) HarborUpdateMetadata(ctx context.Context, ref transfer.TransferRef) error {
	activity.RecordHeartbeat(ctx)
	r := provisioning.ResourceRef{ServiceID: ref.ServiceID, Project: ref.Source, Name: ref.Name}
	return classify(a.harbor.UpdateMetadata(ctx, r, ref.Target))
}

// TransferOwnerRoles переносит роли владельцев между проектами: для каждого
// субъекта-владельца отзывает owner:project:<source> и выдаёт owner:project:<target>
// (инвалидация кэша — на стороне IDM). Идемпотентно.
func (a *Activities) TransferOwnerRoles(ctx context.Context, in transfer.TransferRolesInput) error {
	activity.RecordHeartbeat(ctx)
	srcRole := "owner:project:" + in.Source
	dstRole := "owner:project:" + in.Target
	for _, subject := range in.Owners {
		if err := a.idmRoles.AssignRole(ctx, subject, dstRole); err != nil {
			return classify(err)
		}
		if err := a.idmRoles.RevokeRole(ctx, subject, srcRole); err != nil {
			return classify(err)
		}
	}
	return nil
}

// Register регистрирует все activities под именами из пакетов provisioning,
// changeowners и decommission на переданном worker'е.
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
	// Вывод из эксплуатации.
	reg(a.EnsureLoadDrained, decommission.ActivityEnsureLoadDrained)
	reg(a.GitLabArchive, decommission.ActivityGitLabArchive)
	reg(a.GitLabUnarchive, decommission.ActivityGitLabUnarchive)
	reg(a.HarborSetReadOnly, decommission.ActivityHarborSetReadOnly)
	reg(a.HarborSetWritable, decommission.ActivityHarborSetWritable)
	reg(a.VaultRevokeSecretID, decommission.ActivityVaultRevokeSecretID)
	reg(a.CatalogDecommission, decommission.ActivityCatalogDecommission)
	// Перенос сервиса.
	reg(a.CatalogBeginTransfer, transfer.ActivityCatalogBeginTransfer)
	reg(a.CatalogCommitTransfer, transfer.ActivityCatalogCommitTransfer)
	reg(a.CatalogAbortTransfer, transfer.ActivityCatalogAbortTransfer)
	reg(a.GitLabTransferRepo, transfer.ActivityGitLabTransferRepo)
	reg(a.VaultMigratePaths, transfer.ActivityVaultMigratePaths)
	reg(a.HarborUpdateMetadata, transfer.ActivityHarborUpdateMetadata)
	reg(a.TransferOwnerRoles, transfer.ActivityTransferOwnerRoles)
}
