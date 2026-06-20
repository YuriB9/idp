package activities_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"github.com/YuriB9/idp/pkg/errs"
	"github.com/YuriB9/idp/services/devinfra-worker/internal/activities"
	"github.com/YuriB9/idp/services/devinfra-worker/internal/integrations"
	"github.com/YuriB9/idp/services/projects/changeowners"
	"github.com/YuriB9/idp/services/projects/decommission"
	"github.com/YuriB9/idp/services/projects/provisioning"
	"github.com/YuriB9/idp/services/projects/transfer"
)

// fakeStatus — управляемый стаб StatusStore.
type fakeStatus struct {
	activateErr error
	activated   string
}

func (f *fakeStatus) Activate(_ context.Context, id string) error {
	f.activated = id
	return f.activateErr
}
func (f *fakeStatus) Fail(_ context.Context, id string) error { return nil }

// TestActivities_TransitionActiveOK: успешный переход делегируется в StatusStore.
func TestActivities_TransitionActiveOK(t *testing.T) {
	t.Parallel()
	fs := &fakeStatus{}
	acts := activities.New(integrations.NewMemory().Clients(), fs)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(acts.CatalogTransitionActive)

	_, err := env.ExecuteActivity(acts.CatalogTransitionActive, provisioning.ResourceRef{ServiceID: "svc-id"})
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if fs.activated != "svc-id" {
		t.Fatalf("ожидали Activate(svc-id), получили %q", fs.activated)
	}
}

// TestActivities_ConflictIsNonRetryable: проигранный guarded-CAS (ErrConflict)
// оборачивается в non-retryable ApplicationError → ветка компенсации, не ретрай.
func TestActivities_ConflictIsNonRetryable(t *testing.T) {
	t.Parallel()
	fs := &fakeStatus{activateErr: fmt.Errorf("guarded-CAS: %w", errs.ErrConflict)}
	acts := activities.New(integrations.NewMemory().Clients(), fs)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(acts.CatalogTransitionActive)

	_, err := env.ExecuteActivity(acts.CatalogTransitionActive, provisioning.ResourceRef{ServiceID: "svc-id"})
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) {
		t.Fatalf("ожидали ApplicationError, получили %v", err)
	}
	if !appErr.NonRetryable() {
		t.Fatal("ошибка должна быть non-retryable")
	}
}

// fakeOwners — стаб OwnersStore.
type fakeOwners struct {
	err        error
	gotID      string
	gotDesired []string
	gotVersion int64
}

func (f *fakeOwners) SetOwners(_ context.Context, id string, desired []string, version int64) error {
	f.gotID, f.gotDesired, f.gotVersion = id, desired, version
	return f.err
}

// fakeRoles — стаб RoleAdmin, фиксирует выданные/отозванные роли.
type fakeRoles struct {
	assigned []string
	revoked  []string
	err      error
}

func (f *fakeRoles) AssignRole(_ context.Context, subject, role string) error {
	f.assigned = append(f.assigned, subject+"@"+role)
	return f.err
}
func (f *fakeRoles) RevokeRole(_ context.Context, subject, role string) error {
	f.revoked = append(f.revoked, subject+"@"+role)
	return f.err
}

// TestActivities_GitLabSyncMembers: synchronize делегирует клиенту интеграции.
func TestActivities_GitLabSyncMembers(t *testing.T) {
	t.Parallel()
	mem := integrations.NewMemory()
	acts := activities.New(mem.Clients(), &fakeStatus{})
	ref := provisioning.ResourceRef{ServiceID: "id", Project: "p1", Name: "svc"}

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(acts.GitLabSyncMembers)

	in := changeowners.SyncMembersInput{Ref: ref, Add: []string{"bob"}, Remove: nil}
	if _, err := env.ExecuteActivity(acts.GitLabSyncMembers, in); err != nil {
		t.Fatalf("GitLabSyncMembers: %v", err)
	}
	if !mem.HasMember(ref, "bob") {
		t.Fatal("ожидали, что bob добавлен в участники")
	}
}

// TestActivities_CatalogSetOwnersConflict: конфликт версии оборачивается в
// non-retryable ApplicationError (точка невозврата ретраем не лечится).
func TestActivities_CatalogSetOwnersConflict(t *testing.T) {
	t.Parallel()
	owners := &fakeOwners{err: fmt.Errorf("guarded-CAS: %w", errs.ErrConflict)}
	acts := activities.New(integrations.NewMemory().Clients(), &fakeStatus{}, activities.WithOwners(owners))

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(acts.CatalogSetOwners)

	in := changeowners.CatalogSetOwnersInput{ServiceID: "svc", Desired: []string{"a"}, ExpectedVersion: 1}
	_, err := env.ExecuteActivity(acts.CatalogSetOwners, in)
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) || !appErr.NonRetryable() {
		t.Fatalf("ожидали non-retryable ApplicationError, получили %v", err)
	}
}

// TestActivities_IDMSyncOwnerRoles: выдаёт роли добавленным, отзывает у удалённых.
func TestActivities_IDMSyncOwnerRoles(t *testing.T) {
	t.Parallel()
	roles := &fakeRoles{}
	acts := activities.New(integrations.NewMemory().Clients(), &fakeStatus{}, activities.WithIDMRoles(roles))

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(acts.IDMSyncOwnerRoles)

	in := changeowners.IDMSyncInput{Project: "demo", Add: []string{"bob"}, Remove: []string{"dave"}}
	if _, err := env.ExecuteActivity(acts.IDMSyncOwnerRoles, in); err != nil {
		t.Fatalf("IDMSyncOwnerRoles: %v", err)
	}
	if len(roles.assigned) != 1 || roles.assigned[0] != "bob@owner:project:demo" {
		t.Fatalf("ожидали выдачу bob, получили %v", roles.assigned)
	}
	if len(roles.revoked) != 1 || roles.revoked[0] != "dave@owner:project:demo" {
		t.Fatalf("ожидали отзыв dave, получили %v", roles.revoked)
	}
}

// TestActivities_ProvisionDelegates: activity провизии делегирует клиенту
// интеграции (in-memory) и фиксирует ресурс.
func TestActivities_ProvisionDelegates(t *testing.T) {
	t.Parallel()
	mem := integrations.NewMemory()
	acts := activities.New(mem.Clients(), &fakeStatus{})
	ref := provisioning.ResourceRef{ServiceID: "id", Project: "p1", Name: "svc"}

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(acts.GitLabCreateRepo)

	if _, err := env.ExecuteActivity(acts.GitLabCreateRepo, ref); err != nil {
		t.Fatalf("GitLabCreateRepo: %v", err)
	}
	if !mem.HasRepo(ref) {
		t.Fatal("репозиторий должен быть создан через клиент интеграции")
	}
}

// fakeDecomm — управляемый стаб DecommStore.
type fakeDecomm struct {
	err error
	got string
}

func (f *fakeDecomm) Decommission(_ context.Context, id string) error {
	f.got = id
	return f.err
}

// TestActivities_EnsureLoadDrained: при снятой нагрузке проверка проходит; при
// неснятой — non-retryable ошибка предусловия (отказ до побочных эффектов).
func TestActivities_EnsureLoadDrained(t *testing.T) {
	t.Parallel()
	acts := activities.New(integrations.NewMemory().Clients(), &fakeStatus{})

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(acts.EnsureLoadDrained)
	ref := provisioning.ResourceRef{ServiceID: "svc", Project: "p", Name: "n"}

	if _, err := env.ExecuteActivity(acts.EnsureLoadDrained,
		decommission.EnsureLoadDrainedInput{Ref: ref, LoadDrained: true}); err != nil {
		t.Fatalf("снятая нагрузка: неожиданная ошибка: %v", err)
	}
	if _, err := env.ExecuteActivity(acts.EnsureLoadDrained,
		decommission.EnsureLoadDrainedInput{Ref: ref, LoadDrained: false}); err == nil {
		t.Fatal("ожидали ошибку предусловия при неснятой нагрузке")
	}
}

// TestActivities_DecommissionRevokesAccess: обратные операции отзывают доступ во
// внешних системах (archive/read-only/revoke) и переводят каталог.
func TestActivities_DecommissionRevokesAccess(t *testing.T) {
	t.Parallel()
	mem := integrations.NewMemory()
	fd := &fakeDecomm{}
	acts := activities.New(mem.Clients(), &fakeStatus{}, activities.WithDecommission(fd))

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(acts.GitLabArchive)
	env.RegisterActivity(acts.HarborSetReadOnly)
	env.RegisterActivity(acts.VaultRevokeSecretID)
	env.RegisterActivity(acts.CatalogDecommission)
	ref := provisioning.ResourceRef{ServiceID: "svc", Project: "p", Name: "n"}

	for _, fn := range []any{acts.GitLabArchive, acts.HarborSetReadOnly, acts.VaultRevokeSecretID, acts.CatalogDecommission} {
		if _, err := env.ExecuteActivity(fn, ref); err != nil {
			t.Fatalf("activity: неожиданная ошибка: %v", err)
		}
	}
	if !mem.IsArchived(ref) || !mem.IsHarborReadOnly(ref) || !mem.IsVaultRevoked(ref) {
		t.Fatalf("ожидали отзыв доступа во всех системах: archived=%v ro=%v revoked=%v",
			mem.IsArchived(ref), mem.IsHarborReadOnly(ref), mem.IsVaultRevoked(ref))
	}
	if fd.got != "svc" {
		t.Fatalf("ожидали Decommission(svc), получили %q", fd.got)
	}
}

// fakeTransfer — управляемый стаб TransferStore.
type fakeTransfer struct {
	beginErr  error
	commitErr error
	begun     string
	committed string
	aborted   string
	commitTgt string
	beginTgt  string
}

func (f *fakeTransfer) BeginTransfer(_ context.Context, id, target string) error {
	f.begun, f.beginTgt = id, target
	return f.beginErr
}
func (f *fakeTransfer) CommitTransfer(_ context.Context, id, target string) error {
	f.committed, f.commitTgt = id, target
	return f.commitErr
}
func (f *fakeTransfer) AbortTransfer(_ context.Context, id string) error {
	f.aborted = id
	return nil
}

// TestActivities_TransferDelegates: activities переноса делегируют клиентам
// интеграций (in-memory) и слою каталога, фиксируя целевой проект.
func TestActivities_TransferDelegates(t *testing.T) {
	t.Parallel()
	mem := integrations.NewMemory()
	ft := &fakeTransfer{}
	acts := activities.New(mem.Clients(), &fakeStatus{}, activities.WithTransfer(ft))

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(acts.CatalogBeginTransfer)
	env.RegisterActivity(acts.GitLabTransferRepo)
	env.RegisterActivity(acts.VaultMigratePaths)
	env.RegisterActivity(acts.HarborUpdateMetadata)
	env.RegisterActivity(acts.CatalogCommitTransfer)
	ref := transfer.TransferRef{ServiceID: "svc", Source: "demo", Target: "demo2", Name: "n"}
	catIn := transfer.CatalogTransferInput{ServiceID: "svc", Target: "demo2"}

	if _, err := env.ExecuteActivity(acts.CatalogBeginTransfer, catIn); err != nil {
		t.Fatalf("CatalogBeginTransfer: %v", err)
	}
	for _, fn := range []any{acts.GitLabTransferRepo, acts.VaultMigratePaths, acts.HarborUpdateMetadata} {
		if _, err := env.ExecuteActivity(fn, ref); err != nil {
			t.Fatalf("activity переноса: неожиданная ошибка: %v", err)
		}
	}
	if _, err := env.ExecuteActivity(acts.CatalogCommitTransfer, catIn); err != nil {
		t.Fatalf("CatalogCommitTransfer: %v", err)
	}

	memRef := provisioning.ResourceRef{ServiceID: "svc", Project: "demo", Name: "n"}
	if mem.RepoGroup(memRef) != "demo2" || mem.VaultPath(memRef) != "demo2" || mem.HarborProject(memRef) != "demo2" {
		t.Fatalf("ожидали перенос инфраструктуры в demo2: repo=%q vault=%q harbor=%q",
			mem.RepoGroup(memRef), mem.VaultPath(memRef), mem.HarborProject(memRef))
	}
	if ft.beginTgt != "demo2" || ft.commitTgt != "demo2" {
		t.Fatalf("ожидали begin/commit в target demo2, получили %+v", ft)
	}
}

// TestActivities_TransferOwnerRoles: для каждого владельца выдаётся роль target и
// отзывается роль source.
func TestActivities_TransferOwnerRoles(t *testing.T) {
	t.Parallel()
	roles := &fakeRoles{}
	acts := activities.New(integrations.NewMemory().Clients(), &fakeStatus{}, activities.WithIDMRoles(roles))

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(acts.TransferOwnerRoles)

	in := transfer.TransferRolesInput{Source: "demo", Target: "demo2", Owners: []string{"alice"}}
	if _, err := env.ExecuteActivity(acts.TransferOwnerRoles, in); err != nil {
		t.Fatalf("TransferOwnerRoles: %v", err)
	}
	if len(roles.assigned) != 1 || roles.assigned[0] != "alice@owner:project:demo2" {
		t.Fatalf("ожидали выдачу alice@owner:project:demo2, получили %v", roles.assigned)
	}
	if len(roles.revoked) != 1 || roles.revoked[0] != "alice@owner:project:demo" {
		t.Fatalf("ожидали отзыв alice@owner:project:demo, получили %v", roles.revoked)
	}
}

// TestActivities_CatalogBeginTransferConflictNonRetryable: занятое имя/конфликт →
// non-retryable (workflow уйдёт в ветку компенсации/ошибки).
func TestActivities_CatalogBeginTransferConflictNonRetryable(t *testing.T) {
	t.Parallel()
	ft := &fakeTransfer{beginErr: fmt.Errorf("занято: %w", errs.ErrConflict)}
	acts := activities.New(integrations.NewMemory().Clients(), &fakeStatus{}, activities.WithTransfer(ft))

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(acts.CatalogBeginTransfer)

	_, err := env.ExecuteActivity(acts.CatalogBeginTransfer, transfer.CatalogTransferInput{ServiceID: "svc", Target: "demo2"})
	if err == nil {
		t.Fatal("ожидали ошибку при конфликте")
	}
}
