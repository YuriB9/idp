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

	env.ExecuteWorkflow(provisioning.CreateServiceWorkflow, provisioning.CreateServiceInput{
		ServiceID: "svc-id", Project: "p1", Name: "svc",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	order := m.order()
	// Порядок провизии: GitLab → Harbor → Vault → инъекция → ACTIVE.
	require.Equal(t, []string{
		provisioning.ActivityGitLabCreateRepo,
		provisioning.ActivityHarborCreate,
		provisioning.ActivityVaultSetup,
		provisioning.ActivityInjectSecrets,
		provisioning.ActivityTransitionActive,
	}, order)
	require.False(t, contains(order, provisioning.ActivityTransitionFailed))
	require.False(t, contains(order, provisioning.ActivityGitLabDeleteRepo))
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
