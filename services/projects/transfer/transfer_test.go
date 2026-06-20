package transfer_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"github.com/YuriB9/idp/services/projects/transfer"
)

// mockActs — управляемый набор activities для temporal testsuite. Фиксирует
// порядок вызовов и позволяет инъектировать сбои отдельных шагов.
type mockActs struct {
	mu    sync.Mutex
	calls []string

	beginConflict bool // CatalogBeginTransfer вернёт non-retryable (занятое имя/конфликт)
	gitlabFatal   bool // GitLabTransferRepo вернёт non-retryable (на точке невозврата)
	vaultFatal    bool // VaultMigratePaths вернёт non-retryable (после точки невозврата)
	commitFatal   bool // CatalogCommitTransfer вернёт non-retryable (конфликт CAS после PONR)
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

const fatalType = "TransferFatal"

func (m *mockActs) CatalogBeginTransfer(_ context.Context, _ transfer.CatalogTransferInput) error {
	m.record(transfer.ActivityCatalogBeginTransfer)
	if m.beginConflict {
		return temporal.NewNonRetryableApplicationError("имя занято в target", fatalType, nil)
	}
	return nil
}

func (m *mockActs) CatalogCommitTransfer(_ context.Context, _ transfer.CatalogTransferInput) error {
	m.record(transfer.ActivityCatalogCommitTransfer)
	if m.commitFatal {
		return temporal.NewNonRetryableApplicationError("конфликт guarded-CAS", fatalType, nil)
	}
	return nil
}

func (m *mockActs) CatalogAbortTransfer(_ context.Context, _ transfer.CatalogTransferInput) error {
	m.record(transfer.ActivityCatalogAbortTransfer)
	return nil
}

func (m *mockActs) GitLabTransferRepo(_ context.Context, _ transfer.TransferRef) error {
	m.record(transfer.ActivityGitLabTransferRepo)
	if m.gitlabFatal {
		return temporal.NewNonRetryableApplicationError("gitlab недоступен", fatalType, nil)
	}
	return nil
}

func (m *mockActs) VaultMigratePaths(_ context.Context, _ transfer.TransferRef) error {
	m.record(transfer.ActivityVaultMigratePaths)
	if m.vaultFatal {
		return temporal.NewNonRetryableApplicationError("vault недоступен", fatalType, nil)
	}
	return nil
}

func (m *mockActs) HarborUpdateMetadata(_ context.Context, _ transfer.TransferRef) error {
	m.record(transfer.ActivityHarborUpdateMetadata)
	return nil
}

func (m *mockActs) TransferOwnerRoles(_ context.Context, _ transfer.TransferRolesInput) error {
	m.record(transfer.ActivityTransferOwnerRoles)
	return nil
}

func (m *mockActs) register(env interface {
	RegisterActivityWithOptions(a any, options activity.RegisterOptions)
}) {
	reg := func(fn any, name string) { env.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name}) }
	reg(m.CatalogBeginTransfer, transfer.ActivityCatalogBeginTransfer)
	reg(m.CatalogCommitTransfer, transfer.ActivityCatalogCommitTransfer)
	reg(m.CatalogAbortTransfer, transfer.ActivityCatalogAbortTransfer)
	reg(m.GitLabTransferRepo, transfer.ActivityGitLabTransferRepo)
	reg(m.VaultMigratePaths, transfer.ActivityVaultMigratePaths)
	reg(m.HarborUpdateMetadata, transfer.ActivityHarborUpdateMetadata)
	reg(m.TransferOwnerRoles, transfer.ActivityTransferOwnerRoles)
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func input() transfer.TransferInput {
	return transfer.TransferInput{ServiceID: "svc-1", Source: "demo", Target: "demo2", Name: "n", Owners: []string{"u1"}}
}

// TestTransferWorkflow_HappyPath: все шаги успешны, порядок соблюдён, компенсаций нет.
func TestTransferWorkflow_HappyPath(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	m := &mockActs{}
	m.register(env)

	env.ExecuteWorkflow(transfer.TransferServiceWorkflow, input())

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, []string{
		transfer.ActivityCatalogBeginTransfer,
		transfer.ActivityGitLabTransferRepo,
		transfer.ActivityVaultMigratePaths,
		transfer.ActivityHarborUpdateMetadata,
		transfer.ActivityCatalogCommitTransfer,
		transfer.ActivityTransferOwnerRoles,
	}, m.order())
}

// TestTransferWorkflow_OccupiedNameBeforeSideEffects: занятое имя в target → отказ
// на шаге начала, внешние системы не затронуты, компенсация каталога не нужна.
func TestTransferWorkflow_OccupiedNameBeforeSideEffects(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	m := &mockActs{beginConflict: true}
	m.register(env)

	env.ExecuteWorkflow(transfer.TransferServiceWorkflow, input())

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	require.Equal(t, []string{transfer.ActivityCatalogBeginTransfer}, m.order(), "побочных эффектов быть не должно")
}

// TestTransferWorkflow_CompensateBeforePoint: сбой GitLab transfer (на точке
// невозврата, до её прохождения) → компенсация каталога (transferring→active),
// инфраструктура далее не трогается.
func TestTransferWorkflow_CompensateBeforePoint(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	m := &mockActs{gitlabFatal: true}
	m.register(env)

	env.ExecuteWorkflow(transfer.TransferServiceWorkflow, input())

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	order := m.order()
	require.True(t, contains(order, transfer.ActivityCatalogAbortTransfer), "ожидали компенсацию каталога")
	require.False(t, contains(order, transfer.ActivityVaultMigratePaths), "Vault не должен трогаться при сбое GitLab")
	require.False(t, contains(order, transfer.ActivityCatalogCommitTransfer), "фиксации каталога быть не должно")
}

// TestTransferWorkflow_AlertAfterPoint: сбой миграции Vault ПОСЛЕ точки невозврата
// (репозиторий уже перенесён) — без молчаливого отката (компенсации каталога нет),
// workflow падает (алерт оператору).
func TestTransferWorkflow_AlertAfterPoint(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	m := &mockActs{vaultFatal: true}
	m.register(env)

	env.ExecuteWorkflow(transfer.TransferServiceWorkflow, input())

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	order := m.order()
	require.True(t, contains(order, transfer.ActivityGitLabTransferRepo), "репозиторий должен быть перенесён до сбоя Vault")
	require.False(t, contains(order, transfer.ActivityCatalogAbortTransfer), "после точки невозврата откатов нет")
}

// TestTransferWorkflow_CommitConflictAfterPoint: конфликт guarded-CAS фиксации
// каталога ПОСЛЕ точки невозврата → алерт, без отката инфраструктуры.
func TestTransferWorkflow_CommitConflictAfterPoint(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	m := &mockActs{commitFatal: true}
	m.register(env)

	env.ExecuteWorkflow(transfer.TransferServiceWorkflow, input())

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	order := m.order()
	require.True(t, contains(order, transfer.ActivityCatalogCommitTransfer), "ожидали попытку фиксации каталога")
	require.False(t, contains(order, transfer.ActivityCatalogAbortTransfer), "после точки невозврата откатов нет")
	require.False(t, contains(order, transfer.ActivityTransferOwnerRoles), "роли не переносятся при сбое фиксации")
}
