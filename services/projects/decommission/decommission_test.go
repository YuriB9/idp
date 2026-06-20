package decommission_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"github.com/YuriB9/idp/services/projects/decommission"
	"github.com/YuriB9/idp/services/projects/provisioning"
)

// mockActs — управляемый набор activities для temporal testsuite. Фиксирует
// порядок вызовов и позволяет инъектировать сбои отдельных шагов.
type mockActs struct {
	mu    sync.Mutex
	calls []string

	loadNotDrained bool // EnsureLoadDrained вернёт non-retryable (предусловие)
	harborFatal    bool // HarborSetReadOnly вернёт non-retryable → компенсация GitLab
	vaultFatal     bool // VaultRevokeSecretID вернёт non-retryable (до точки невозврата)
	catalogFatal   bool // CatalogDecommission вернёт non-retryable (после точки невозврата)
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

const fatalType = "DecommissionFatal"

func (m *mockActs) EnsureLoadDrained(_ context.Context, _ decommission.EnsureLoadDrainedInput) error {
	m.record(decommission.ActivityEnsureLoadDrained)
	if m.loadNotDrained {
		return temporal.NewNonRetryableApplicationError("нагрузка не снята", fatalType, nil)
	}
	return nil
}

func (m *mockActs) GitLabArchive(_ context.Context, _ provisioning.ResourceRef) error {
	m.record(decommission.ActivityGitLabArchive)
	return nil
}

func (m *mockActs) GitLabUnarchive(_ context.Context, _ provisioning.ResourceRef) error {
	m.record(decommission.ActivityGitLabUnarchive)
	return nil
}

func (m *mockActs) HarborSetReadOnly(_ context.Context, _ provisioning.ResourceRef) error {
	m.record(decommission.ActivityHarborSetReadOnly)
	if m.harborFatal {
		return temporal.NewNonRetryableApplicationError("harbor недоступен", fatalType, nil)
	}
	return nil
}

func (m *mockActs) HarborSetWritable(_ context.Context, _ provisioning.ResourceRef) error {
	m.record(decommission.ActivityHarborSetWritable)
	return nil
}

func (m *mockActs) VaultRevokeSecretID(_ context.Context, _ provisioning.ResourceRef) error {
	m.record(decommission.ActivityVaultRevokeSecretID)
	if m.vaultFatal {
		return temporal.NewNonRetryableApplicationError("vault недоступен", fatalType, nil)
	}
	return nil
}

func (m *mockActs) CatalogDecommission(_ context.Context, _ provisioning.ResourceRef) error {
	m.record(decommission.ActivityCatalogDecommission)
	if m.catalogFatal {
		return temporal.NewNonRetryableApplicationError("конфликт статуса", fatalType, nil)
	}
	return nil
}

func (m *mockActs) register(env interface {
	RegisterActivityWithOptions(a any, options activity.RegisterOptions)
}) {
	reg := func(fn any, name string) { env.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name}) }
	reg(m.EnsureLoadDrained, decommission.ActivityEnsureLoadDrained)
	reg(m.GitLabArchive, decommission.ActivityGitLabArchive)
	reg(m.GitLabUnarchive, decommission.ActivityGitLabUnarchive)
	reg(m.HarborSetReadOnly, decommission.ActivityHarborSetReadOnly)
	reg(m.HarborSetWritable, decommission.ActivityHarborSetWritable)
	reg(m.VaultRevokeSecretID, decommission.ActivityVaultRevokeSecretID)
	reg(m.CatalogDecommission, decommission.ActivityCatalogDecommission)
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func input() decommission.DecommissionInput {
	return decommission.DecommissionInput{ServiceID: "svc-1", Project: "p", Name: "n", LoadDrained: true}
}

// TestDecommissionWorkflow_HappyPath: все шаги успешны, порядок соблюдён,
// компенсаций нет.
func TestDecommissionWorkflow_HappyPath(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	m := &mockActs{}
	m.register(env)

	env.ExecuteWorkflow(decommission.DecommissionWorkflow, input())

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, []string{
		decommission.ActivityEnsureLoadDrained,
		decommission.ActivityGitLabArchive,
		decommission.ActivityHarborSetReadOnly,
		decommission.ActivityVaultRevokeSecretID,
		decommission.ActivityCatalogDecommission,
	}, m.order())
}

// TestDecommissionWorkflow_PreconditionNotMet: нагрузка не снята → отказ до любых
// побочных эффектов (отзывов доступа и каталога нет).
func TestDecommissionWorkflow_PreconditionNotMet(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	m := &mockActs{loadNotDrained: true}
	m.register(env)

	in := input()
	in.LoadDrained = false
	env.ExecuteWorkflow(decommission.DecommissionWorkflow, in)

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	order := m.order()
	require.Equal(t, []string{decommission.ActivityEnsureLoadDrained}, order, "побочных эффектов быть не должно")
}

// TestDecommissionWorkflow_CompensateBeforePoint: сбой Harbor до точки невозврата
// (Vault) запускает компенсацию GitLab; каталог не трогается; workflow падает.
func TestDecommissionWorkflow_CompensateBeforePoint(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	m := &mockActs{harborFatal: true}
	m.register(env)

	env.ExecuteWorkflow(decommission.DecommissionWorkflow, input())

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	order := m.order()
	require.True(t, contains(order, decommission.ActivityGitLabUnarchive), "ожидали компенсацию GitLab")
	require.False(t, contains(order, decommission.ActivityVaultRevokeSecretID), "необратимый отзыв Vault не должен выполняться")
	require.False(t, contains(order, decommission.ActivityCatalogDecommission), "каталог не должен меняться")
}

// TestDecommissionWorkflow_AlertAfterPoint: сбой каталога ПОСЛЕ точки невозврата
// (Vault уже отозван) — без молчаливого отката (компенсаций GitLab/Harbor нет),
// workflow падает (алерт оператору).
func TestDecommissionWorkflow_AlertAfterPoint(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	m := &mockActs{catalogFatal: true}
	m.register(env)

	env.ExecuteWorkflow(decommission.DecommissionWorkflow, input())

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	order := m.order()
	require.True(t, contains(order, decommission.ActivityVaultRevokeSecretID), "доступ Vault должен быть отозван до сбоя каталога")
	require.False(t, contains(order, decommission.ActivityGitLabUnarchive), "после точки невозврата откатов нет")
	require.False(t, contains(order, decommission.ActivityHarborSetWritable), "после точки невозврата откатов нет")
}

// TestDecommissionWorkflow_VaultFatalBeforePoint: сбой Vault (на самой точке
// невозврата, до её прохождения) → компенсации Harbor и GitLab, каталог не трогается.
func TestDecommissionWorkflow_VaultFatalBeforePoint(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	m := &mockActs{vaultFatal: true}
	m.register(env)

	env.ExecuteWorkflow(decommission.DecommissionWorkflow, input())

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	order := m.order()
	require.True(t, contains(order, decommission.ActivityHarborSetWritable), "ожидали компенсацию Harbor")
	require.True(t, contains(order, decommission.ActivityGitLabUnarchive), "ожидали компенсацию GitLab")
	require.False(t, contains(order, decommission.ActivityCatalogDecommission), "каталог не должен меняться")
}
