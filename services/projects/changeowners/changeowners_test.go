package changeowners_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"github.com/YuriB9/idp/services/projects/changeowners"
)

// mockActs — управляемый набор activities для temporal testsuite. Фиксирует
// порядок вызовов и позволяет инъектировать сбои отдельных шагов.
type mockActs struct {
	mu    sync.Mutex
	calls []string

	vaultFatal   bool // VaultSyncOwners вернёт non-retryable → компенсация GitLab
	catalogFatal bool // CatalogSetOwners вернёт non-retryable (конфликт версии)
	idmFatal     bool // IDMSyncOwnerRoles вернёт non-retryable (после точки невозврата)
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

func (m *mockActs) GitLabSyncMembers(_ context.Context, _ changeowners.SyncMembersInput) error {
	m.record(changeowners.ActivityGitLabSyncMembers)
	return nil
}

func (m *mockActs) GitLabRestoreMembers(_ context.Context, _ changeowners.RestoreInput) error {
	m.record(changeowners.ActivityGitLabRestoreMembers)
	return nil
}

func (m *mockActs) VaultSyncOwners(_ context.Context, _ changeowners.SyncMembersInput) error {
	m.record(changeowners.ActivityVaultSyncOwners)
	if m.vaultFatal {
		return temporal.NewNonRetryableApplicationError("vault недоступен", "ChangeOwnersFatal", nil)
	}
	return nil
}

func (m *mockActs) VaultRestoreOwners(_ context.Context, _ changeowners.RestoreInput) error {
	m.record(changeowners.ActivityVaultRestoreOwners)
	return nil
}

func (m *mockActs) CatalogSetOwners(_ context.Context, _ changeowners.CatalogSetOwnersInput) error {
	m.record(changeowners.ActivityCatalogSetOwners)
	if m.catalogFatal {
		return temporal.NewNonRetryableApplicationError("конфликт версии", "ChangeOwnersFatal", nil)
	}
	return nil
}

func (m *mockActs) IDMSyncOwnerRoles(_ context.Context, _ changeowners.IDMSyncInput) error {
	m.record(changeowners.ActivityIDMSyncOwnerRoles)
	if m.idmFatal {
		return temporal.NewNonRetryableApplicationError("idm недоступен", "ChangeOwnersFatal", nil)
	}
	return nil
}

func (m *mockActs) register(env interface {
	RegisterActivityWithOptions(a any, options activity.RegisterOptions)
}) {
	reg := func(fn any, name string) { env.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name}) }
	reg(m.GitLabSyncMembers, changeowners.ActivityGitLabSyncMembers)
	reg(m.GitLabRestoreMembers, changeowners.ActivityGitLabRestoreMembers)
	reg(m.VaultSyncOwners, changeowners.ActivityVaultSyncOwners)
	reg(m.VaultRestoreOwners, changeowners.ActivityVaultRestoreOwners)
	reg(m.CatalogSetOwners, changeowners.ActivityCatalogSetOwners)
	reg(m.IDMSyncOwnerRoles, changeowners.ActivityIDMSyncOwnerRoles)
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

func input() changeowners.ChangeOwnersInput {
	return changeowners.ChangeOwnersInput{
		ServiceID:       "svc-1",
		Project:         "p",
		Name:            "n",
		Previous:        []string{"alice"},
		Desired:         []string{"alice", "bob"},
		ExpectedVersion: 1,
	}
}

// TestChangeOwnersWorkflow_HappyPath: все шаги успешны, порядок соблюдён,
// компенсации не вызываются.
func TestChangeOwnersWorkflow_HappyPath(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	m := &mockActs{}
	m.register(env)

	env.ExecuteWorkflow(changeowners.ChangeOwnersWorkflow, input())

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	order := m.order()
	require.Equal(t, []string{
		changeowners.ActivityGitLabSyncMembers,
		changeowners.ActivityVaultSyncOwners,
		changeowners.ActivityCatalogSetOwners,
		changeowners.ActivityIDMSyncOwnerRoles,
	}, order)
}

// TestChangeOwnersWorkflow_CompensateBeforePoint: сбой Vault до точки невозврата
// запускает компенсацию GitLab; каталог и IDM не трогаются; workflow падает.
func TestChangeOwnersWorkflow_CompensateBeforePoint(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	m := &mockActs{vaultFatal: true}
	m.register(env)

	env.ExecuteWorkflow(changeowners.ChangeOwnersWorkflow, input())

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	order := m.order()
	require.True(t, contains(order, changeowners.ActivityGitLabRestoreMembers), "ожидали компенсацию GitLab")
	require.False(t, contains(order, changeowners.ActivityCatalogSetOwners), "каталог не должен меняться до компенсации")
	require.False(t, contains(order, changeowners.ActivityIDMSyncOwnerRoles), "IDM не должен синхронизироваться")
}

// TestChangeOwnersWorkflow_CatalogConflict: конфликт guarded-CAS на записи
// владельцев (до точки невозврата) → компенсации GitLab и Vault, IDM не трогается.
func TestChangeOwnersWorkflow_CatalogConflict(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	m := &mockActs{catalogFatal: true}
	m.register(env)

	env.ExecuteWorkflow(changeowners.ChangeOwnersWorkflow, input())

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	order := m.order()
	require.True(t, contains(order, changeowners.ActivityGitLabRestoreMembers))
	require.True(t, contains(order, changeowners.ActivityVaultRestoreOwners))
	require.False(t, contains(order, changeowners.ActivityIDMSyncOwnerRoles))
}

// TestChangeOwnersWorkflow_IDMFailAfterPoint: сбой IDM ПОСЛЕ точки невозврата —
// без молчаливого отката (компенсаций GitLab/Vault нет), workflow падает.
func TestChangeOwnersWorkflow_IDMFailAfterPoint(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	m := &mockActs{idmFatal: true}
	m.register(env)

	env.ExecuteWorkflow(changeowners.ChangeOwnersWorkflow, input())

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	order := m.order()
	require.True(t, contains(order, changeowners.ActivityCatalogSetOwners), "каталог должен быть записан до сбоя IDM")
	require.Equal(t, -1, indexOf(order, changeowners.ActivityGitLabRestoreMembers), "после точки невозврата откатов нет")
	require.Equal(t, -1, indexOf(order, changeowners.ActivityVaultRestoreOwners), "после точки невозврата откатов нет")
}

// TestChangeOwnersWorkflow_NoOp: пустой diff → workflow завершается успешно без
// обращения к activities.
func TestChangeOwnersWorkflow_NoOp(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	m := &mockActs{}
	m.register(env)

	in := input()
	in.Previous = []string{"alice", "bob"}
	in.Desired = []string{"bob", "alice"}
	env.ExecuteWorkflow(changeowners.ChangeOwnersWorkflow, in)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.Empty(t, m.order(), "при пустом diff activities не вызываются")
}
