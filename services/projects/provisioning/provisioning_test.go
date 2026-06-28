package provisioning_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"github.com/YuriB9/idp/services/projects/provisioning"
)

// mockActs — управляемый набор activities для temporal testsuite. Фиксирует
// порядок вызовов и позволяет инъектировать сбои отдельных шагов.
type mockActs struct {
	mu    sync.Mutex
	calls []string

	gitlabFailFirst int  // сколько первых попыток CreateRepo вернут transient-ошибку
	gitlabAttempts  int  // счётчик попыток CreateRepo
	vaultFatal      bool // VaultSetupAppRole вернёт non-retryable → ветка компенсации
	harborDelFail   bool // компенсация HarborDelete упадёт (проверка alert+FAILED)
	gitlabSyncFatal bool // GitLabSyncMembers вернёт non-retryable → компенсация
	vaultSyncFatal  bool // VaultSyncOwners вернёт non-retryable → компенсация
	idmFatal        bool // IDMSyncOwnerRoles (после активации) вернёт non-retryable

	// Захваченные владельцы из шагов назначения (для проверки add=owners).
	gitlabOwners []string
	vaultOwners  []string
	idmOwners    []string
}

func (m *mockActs) record(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, name)
}

func (m *mockActs) order() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.calls))
	copy(out, m.calls)
	return out
}

func (m *mockActs) GitLabCreateRepo(_ context.Context, ref provisioning.ResourceRef) (provisioning.GitLabRepo, error) {
	m.record(provisioning.ActivityGitLabCreateRepo)
	m.mu.Lock()
	m.gitlabAttempts++
	attempt := m.gitlabAttempts
	m.mu.Unlock()
	if attempt <= m.gitlabFailFirst {
		return provisioning.GitLabRepo{}, errors.New("временный сбой сети GitLab")
	}
	return provisioning.GitLabRepo{ProjectID: "id", Path: ref.Project + "/" + ref.Name}, nil
}

func (m *mockActs) GitLabDeleteRepo(_ context.Context, _ provisioning.ResourceRef) error {
	m.record(provisioning.ActivityGitLabDeleteRepo)
	return nil
}

func (m *mockActs) GitLabInjectSecrets(_ context.Context, _ provisioning.InjectSecretsInput) error {
	m.record(provisioning.ActivityInjectSecrets)
	return nil
}

func (m *mockActs) HarborCreateProject(_ context.Context, _ provisioning.ResourceRef) (provisioning.HarborResult, error) {
	m.record(provisioning.ActivityHarborCreate)
	return provisioning.HarborResult{ProjectName: "p", RobotName: "r", RobotToken: "t"}, nil
}

func (m *mockActs) HarborDeleteProject(_ context.Context, _ provisioning.ResourceRef) error {
	m.record(provisioning.ActivityHarborDelete)
	if m.harborDelFail {
		return errors.New("компенсация Harbor не удалась")
	}
	return nil
}

func (m *mockActs) VaultSetupAppRole(_ context.Context, _ provisioning.ResourceRef) (provisioning.VaultResult, error) {
	m.record(provisioning.ActivityVaultSetup)
	if m.vaultFatal {
		return provisioning.VaultResult{}, temporal.NewNonRetryableApplicationError("vault недоступен", "ProvisioningFatal", nil)
	}
	return provisioning.VaultResult{RoleID: "role", SecretID: "secret"}, nil
}

func (m *mockActs) VaultTeardownAppRole(_ context.Context, _ provisioning.ResourceRef) error {
	m.record(provisioning.ActivityVaultTeardown)
	return nil
}

func (m *mockActs) CatalogTransitionActive(_ context.Context, _ provisioning.ResourceRef) error {
	m.record(provisioning.ActivityTransitionActive)
	return nil
}

func (m *mockActs) CatalogTransitionFailed(_ context.Context, _ provisioning.ResourceRef) error {
	m.record(provisioning.ActivityTransitionFailed)
	return nil
}

func (m *mockActs) GitLabSyncMembers(_ context.Context, in provisioning.SyncOwnersInput) error {
	m.record(provisioning.ActivityGitLabSyncMembers)
	m.mu.Lock()
	m.gitlabOwners = in.Add
	m.mu.Unlock()
	if m.gitlabSyncFatal {
		return temporal.NewNonRetryableApplicationError("назначение владельцев GitLab не удалось", "ProvisioningFatal", nil)
	}
	return nil
}

func (m *mockActs) VaultSyncOwners(_ context.Context, in provisioning.SyncOwnersInput) error {
	m.record(provisioning.ActivityVaultSyncOwners)
	m.mu.Lock()
	m.vaultOwners = in.Add
	m.mu.Unlock()
	if m.vaultSyncFatal {
		return temporal.NewNonRetryableApplicationError("выдача политик Vault не удалась", "ProvisioningFatal", nil)
	}
	return nil
}

func (m *mockActs) IDMSyncOwnerRoles(_ context.Context, in provisioning.IDMSyncOwnersInput) error {
	m.record(provisioning.ActivityIDMSyncOwnerRoles)
	m.mu.Lock()
	m.idmOwners = in.Add
	m.mu.Unlock()
	if m.idmFatal {
		return temporal.NewNonRetryableApplicationError("выдача ролей IDM не удалась", "ProvisioningFatal", nil)
	}
	return nil
}

// register регистрирует mock-activities под именами контракта.
func (m *mockActs) register(env interface {
	RegisterActivityWithOptions(a any, options activity.RegisterOptions)
}) {
	reg := func(fn any, name string) { env.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name}) }
	reg(m.GitLabCreateRepo, provisioning.ActivityGitLabCreateRepo)
	reg(m.GitLabDeleteRepo, provisioning.ActivityGitLabDeleteRepo)
	reg(m.GitLabInjectSecrets, provisioning.ActivityInjectSecrets)
	reg(m.HarborCreateProject, provisioning.ActivityHarborCreate)
	reg(m.HarborDeleteProject, provisioning.ActivityHarborDelete)
	reg(m.VaultSetupAppRole, provisioning.ActivityVaultSetup)
	reg(m.VaultTeardownAppRole, provisioning.ActivityVaultTeardown)
	reg(m.CatalogTransitionActive, provisioning.ActivityTransitionActive)
	reg(m.CatalogTransitionFailed, provisioning.ActivityTransitionFailed)
	reg(m.GitLabSyncMembers, provisioning.ActivityGitLabSyncMembers)
	reg(m.VaultSyncOwners, provisioning.ActivityVaultSyncOwners)
	reg(m.IDMSyncOwnerRoles, provisioning.ActivityIDMSyncOwnerRoles)
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// indexOf возвращает позицию первого вхождения (или -1).
func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// TestCreateServiceWorkflow_HappyPath: все activities успешны, порядок провизии
// соблюдён, финальный переход — ACTIVE, компенсации не вызываются.
func TestCreateServiceWorkflow_HappyPath(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	m := &mockActs{}
	m.register(env)

	owners := []string{"alice", "bob"}
	env.ExecuteWorkflow(provisioning.CreateServiceWorkflow, provisioning.CreateServiceInput{
		ServiceID: "svc-id", Project: "p1", Name: "svc", Owners: owners,
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	order := m.order()
	// Порядок провизии (вариант B): GitLab repo → GitLab members → Harbor →
	// Vault setup → Vault policies → инъекция → ACTIVE → IDM roles.
	require.Equal(t, []string{
		provisioning.ActivityGitLabCreateRepo,
		provisioning.ActivityGitLabSyncMembers,
		provisioning.ActivityHarborCreate,
		provisioning.ActivityVaultSetup,
		provisioning.ActivityVaultSyncOwners,
		provisioning.ActivityInjectSecrets,
		provisioning.ActivityTransitionActive,
		provisioning.ActivityIDMSyncOwnerRoles,
	}, order)
	require.False(t, contains(order, provisioning.ActivityTransitionFailed))
	require.False(t, contains(order, provisioning.ActivityGitLabDeleteRepo))
	// Владельцы из входа доводятся до всех шагов назначения (add=owners).
	require.Equal(t, owners, m.gitlabOwners)
	require.Equal(t, owners, m.vaultOwners)
	require.Equal(t, owners, m.idmOwners)
}

// TestCreateServiceWorkflow_RetryThenSuccess: транзиентные сбои GitLab
// повторяются по RetryPolicy, после чего workflow завершается успешно.
func TestCreateServiceWorkflow_RetryThenSuccess(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	m := &mockActs{gitlabFailFirst: 2}
	m.register(env)

	env.ExecuteWorkflow(provisioning.CreateServiceWorkflow, provisioning.CreateServiceInput{
		ServiceID: "svc-id", Project: "p1", Name: "svc",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.GreaterOrEqual(t, m.gitlabAttempts, 3, "ожидали ретраи CreateRepo")
	require.True(t, contains(m.order(), provisioning.ActivityTransitionActive))
}

// TestCreateServiceWorkflow_VaultFatalCompensates: non-retryable сбой Vault →
// компенсации в обратном порядке (Harbor, затем GitLab) и перевод в FAILED.
func TestCreateServiceWorkflow_VaultFatalCompensates(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	m := &mockActs{vaultFatal: true}
	m.register(env)

	env.ExecuteWorkflow(provisioning.CreateServiceWorkflow, provisioning.CreateServiceInput{
		ServiceID: "svc-id", Project: "p1", Name: "svc",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())

	order := m.order()
	// Компенсации выполнены и именно в обратном порядке: Harbor раньше GitLab.
	require.True(t, contains(order, provisioning.ActivityHarborDelete))
	require.True(t, contains(order, provisioning.ActivityGitLabDeleteRepo))
	require.Less(t, indexOf(order, provisioning.ActivityHarborDelete), indexOf(order, provisioning.ActivityGitLabDeleteRepo))
	// Vault AppRole не создан → его компенсация не вызывается.
	require.False(t, contains(order, provisioning.ActivityVaultTeardown))
	// Запись переведена в FAILED.
	require.True(t, contains(order, provisioning.ActivityTransitionFailed))
	// ACTIVE не вызывается при сбое.
	require.False(t, contains(order, provisioning.ActivityTransitionActive))
}

// TestCreateServiceWorkflow_CompensationFailureStillFails: даже если сама
// компенсация (Harbor) падает, запись всё равно переводится в FAILED (alert
// оператору в логе), workflow завершается ошибкой — сбой не проглатывается.
func TestCreateServiceWorkflow_CompensationFailureStillFails(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	m := &mockActs{vaultFatal: true, harborDelFail: true}
	m.register(env)

	env.ExecuteWorkflow(provisioning.CreateServiceWorkflow, provisioning.CreateServiceInput{
		ServiceID: "svc-id", Project: "p1", Name: "svc",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	require.True(t, contains(m.order(), provisioning.ActivityTransitionFailed))
}

// TestCreateServiceWorkflow_VaultSyncOwnersFatalCompensates: non-retryable сбой
// шага выдачи политик владельцам Vault (после Vault setup) → полный откат
// (teardown Vault, удаление Harbor, удаление GitLab) и перевод в FAILED. ACTIVE и
// IDM не вызываются.
func TestCreateServiceWorkflow_VaultSyncOwnersFatalCompensates(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	m := &mockActs{vaultSyncFatal: true}
	m.register(env)

	env.ExecuteWorkflow(provisioning.CreateServiceWorkflow, provisioning.CreateServiceInput{
		ServiceID: "svc-id", Project: "p1", Name: "svc", Owners: []string{"alice"},
	})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())

	order := m.order()
	// Компенсации в обратном порядке: Vault teardown → Harbor delete → GitLab delete.
	require.True(t, contains(order, provisioning.ActivityVaultTeardown))
	require.True(t, contains(order, provisioning.ActivityHarborDelete))
	require.True(t, contains(order, provisioning.ActivityGitLabDeleteRepo))
	require.Less(t, indexOf(order, provisioning.ActivityVaultTeardown), indexOf(order, provisioning.ActivityHarborDelete))
	require.Less(t, indexOf(order, provisioning.ActivityHarborDelete), indexOf(order, provisioning.ActivityGitLabDeleteRepo))
	require.True(t, contains(order, provisioning.ActivityTransitionFailed))
	require.False(t, contains(order, provisioning.ActivityTransitionActive))
	require.False(t, contains(order, provisioning.ActivityIDMSyncOwnerRoles))
}

// TestCreateServiceWorkflow_IDMRolesFatalKeepsActive: сбой выдачи ролей IDM ПОСЛЕ
// активации не откатывает созданное (каталог — источник правды, ADR-0005/0008):
// запись остаётся ACTIVE (TransitionFailed и компенсации не вызываются), но
// workflow завершается ошибкой (сбой не проглатывается, алерт оператору).
func TestCreateServiceWorkflow_IDMRolesFatalKeepsActive(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	m := &mockActs{idmFatal: true}
	m.register(env)

	env.ExecuteWorkflow(provisioning.CreateServiceWorkflow, provisioning.CreateServiceInput{
		ServiceID: "svc-id", Project: "p1", Name: "svc", Owners: []string{"alice"},
	})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())

	order := m.order()
	require.True(t, contains(order, provisioning.ActivityTransitionActive))
	require.True(t, contains(order, provisioning.ActivityIDMSyncOwnerRoles))
	// Без отката: запись не переводится в FAILED, ресурсы не удаляются.
	require.False(t, contains(order, provisioning.ActivityTransitionFailed))
	require.False(t, contains(order, provisioning.ActivityGitLabDeleteRepo))
	require.False(t, contains(order, provisioning.ActivityHarborDelete))
	require.False(t, contains(order, provisioning.ActivityVaultTeardown))
}
