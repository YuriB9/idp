package integrations_test

import (
	"context"
	"testing"

	"go.uber.org/goleak"

	"github.com/YuriB9/idp/services/devinfra-worker/internal/integrations"
	"github.com/YuriB9/idp/services/projects/provisioning"
)

// TestMain включает goleak: пакет проверяет отсутствие утечек горутин.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// TestMemory_IdempotentProvisionAndCompensate проверяет идемпотентность операций
// провизии и компенсаций in-memory клиентов: повторная провизия не порождает
// ошибки, повторная компенсация удалённого ресурса — успешный no-op.
func TestMemory_IdempotentProvisionAndCompensate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := integrations.NewMemory()
	c := mem.Clients()
	ref := provisioning.ResourceRef{ServiceID: "id", Project: "p1", Name: "svc"}

	// Повторная провизия идемпотентна.
	for range 2 {
		if _, err := c.GitLab.CreateRepo(ctx, ref); err != nil {
			t.Fatalf("CreateRepo: %v", err)
		}
		if _, err := c.Harbor.CreateProject(ctx, ref); err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		if _, err := c.Vault.SetupAppRole(ctx, ref); err != nil {
			t.Fatalf("SetupAppRole: %v", err)
		}
	}
	if !mem.HasRepo(ref) || !mem.HasHarbor(ref) || !mem.HasVault(ref) {
		t.Fatal("ресурсы должны существовать после провизии")
	}

	// Повторная компенсация удалённого ресурса — успешный no-op.
	for range 2 {
		if err := c.Vault.TeardownAppRole(ctx, ref); err != nil {
			t.Fatalf("TeardownAppRole: %v", err)
		}
		if err := c.Harbor.DeleteProject(ctx, ref); err != nil {
			t.Fatalf("DeleteProject: %v", err)
		}
		if err := c.GitLab.DeleteRepo(ctx, ref); err != nil {
			t.Fatalf("DeleteRepo: %v", err)
		}
	}
	if mem.HasRepo(ref) || mem.HasHarbor(ref) || mem.HasVault(ref) {
		t.Fatal("ресурсы должны отсутствовать после компенсации")
	}
}
