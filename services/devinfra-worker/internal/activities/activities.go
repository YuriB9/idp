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
	"github.com/YuriB9/idp/services/projects/provisioning"
)

// StatusStore — зависимость финальных activities от слоя каталога (guarded-CAS).
type StatusStore interface {
	Activate(ctx context.Context, serviceID string) error
	Fail(ctx context.Context, serviceID string) error
}

// Activities связывает клиентов интеграций и слой каталога с activity-методами.
type Activities struct {
	gitlab integrations.GitLab
	harbor integrations.Harbor
	vault  integrations.Vault
	status StatusStore
}

// New собирает набор activities.
func New(clients *integrations.Clients, status StatusStore) *Activities {
	return &Activities{gitlab: clients.GitLab, harbor: clients.Harbor, vault: clients.Vault, status: status}
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

// Register регистрирует все activities под именами из пакета provisioning на
// переданном worker'е.
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
}
