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
	"github.com/YuriB9/idp/services/projects/provisioning"
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
