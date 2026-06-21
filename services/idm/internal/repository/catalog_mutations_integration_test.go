//go:build integration

// Интеграционные тесты структурных мутаций каталога RBAC (ADR-0015) против
// реального PostgreSQL. Запуск:
//
//	go test -tags=integration ./internal/repository/...
//
// DSN из IDM_TEST_DSN (по умолчанию — локальный postgres-idm); при отсутствии БД
// тест помечается Skip (см. testPool). Создаваемые сущности имеют уникальные
// имена и удаляются в defer, чтобы не загрязнять засеянный каталог.
package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/YuriB9/idp/pkg/errs"
)

// TestIntegrationCreateDeleteRole проверяет создание роли (system=false),
// конфликт дубля, удаление и NotFound для отсутствующей.
func TestIntegrationCreateDeleteRole(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	repo := New(pool)
	ctx := context.Background()
	const role = "it-dyn-role"
	_, _ = pool.Exec(ctx, `DELETE FROM roles WHERE name=$1`, role)
	defer func() { _, _ = pool.Exec(ctx, `DELETE FROM roles WHERE name=$1`, role) }()

	created, err := repo.CreateRole(ctx, role)
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if created.Name != role || created.System {
		t.Fatalf("ожидали пользовательскую роль %q (system=false), получили %+v", role, created)
	}

	// Дубль имени → ErrConflict.
	if _, err := repo.CreateRole(ctx, role); !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("ожидали ErrConflict для дубля роли, получили %v", err)
	}

	// Удаление пользовательской роли — успех.
	if err := repo.DeleteRole(ctx, role); err != nil {
		t.Fatalf("DeleteRole: %v", err)
	}

	// Повторное удаление → NotFound.
	if err := repo.DeleteRole(ctx, role); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("ожидали ErrNotFound для удаления отсутствующей роли, получили %v", err)
	}
}

// TestIntegrationDeleteSystemRoleForbidden проверяет защиту системной роли от
// удаления (ErrPrecondition). Создаёт временную роль и помечает её system=true.
func TestIntegrationDeleteSystemRoleForbidden(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	repo := New(pool)
	ctx := context.Background()
	const role = "it-sys-role"
	_, _ = pool.Exec(ctx, `DELETE FROM roles WHERE name=$1`, role)
	if _, err := pool.Exec(ctx, `INSERT INTO roles (name, system) VALUES ($1, true)`, role); err != nil {
		t.Fatalf("вставка системной роли: %v", err)
	}
	defer func() { _, _ = pool.Exec(ctx, `DELETE FROM roles WHERE name=$1`, role) }()

	if err := repo.DeleteRole(ctx, role); !errors.Is(err, errs.ErrPrecondition) {
		t.Fatalf("ожидали ErrPrecondition для удаления системной роли, получили %v", err)
	}
}

// TestIntegrationCreateDeletePermission проверяет создание права (system=false),
// конфликт дубля, удаление и NotFound.
func TestIntegrationCreateDeletePermission(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	repo := New(pool)
	ctx := context.Background()
	const action, resource = "it-dyn-act", "it-dyn-res"
	_, _ = pool.Exec(ctx, `DELETE FROM permissions WHERE action=$1 AND resource=$2`, action, resource)
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM permissions WHERE action=$1 AND resource=$2`, action, resource)
	}()

	p, err := repo.CreatePermission(ctx, action, resource)
	if err != nil {
		t.Fatalf("CreatePermission: %v", err)
	}
	if p.System {
		t.Fatalf("ожидали пользовательское право (system=false), получили %+v", p)
	}

	if _, err := repo.CreatePermission(ctx, action, resource); !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("ожидали ErrConflict для дубля права, получили %v", err)
	}

	if err := repo.DeletePermission(ctx, action, resource); err != nil {
		t.Fatalf("DeletePermission: %v", err)
	}
	if err := repo.DeletePermission(ctx, action, resource); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("ожидали ErrNotFound для удаления отсутствующего права, получили %v", err)
	}
}

// TestIntegrationAttachDetachPermission проверяет идемпотентный attach/detach,
// NotFound для отсутствующих роли/права и защиту системной роли.
func TestIntegrationAttachDetachPermission(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	repo := New(pool)
	ctx := context.Background()
	const role, sysRole = "it-attach-role", "it-attach-sys"
	const action, resource = "it-attach-act", "it-attach-res"

	_, _ = pool.Exec(ctx, `DELETE FROM roles WHERE name IN ($1,$2)`, role, sysRole)
	_, _ = pool.Exec(ctx, `DELETE FROM permissions WHERE action=$1 AND resource=$2`, action, resource)
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM roles WHERE name IN ($1,$2)`, role, sysRole)
		_, _ = pool.Exec(ctx, `DELETE FROM permissions WHERE action=$1 AND resource=$2`, action, resource)
	}()

	if _, err := repo.CreateRole(ctx, role); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if _, err := repo.CreatePermission(ctx, action, resource); err != nil {
		t.Fatalf("CreatePermission: %v", err)
	}

	// Attach к несуществующей роли → NotFound.
	if _, err := repo.AttachPermission(ctx, "no-such-role", action, resource); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("ожидали ErrNotFound (роль), получили %v", err)
	}
	// Attach несуществующего права → NotFound.
	if _, err := repo.AttachPermission(ctx, role, "no-act", "no-res"); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("ожидали ErrNotFound (право), получили %v", err)
	}

	// Успешный attach + идемпотентный повтор.
	perms, err := repo.AttachPermission(ctx, role, action, resource)
	if err != nil {
		t.Fatalf("AttachPermission: %v", err)
	}
	if !containsPerm(perms, action, resource) {
		t.Fatalf("ожидали право в наборе роли после attach, получили %+v", perms)
	}
	if _, err := repo.AttachPermission(ctx, role, action, resource); err != nil {
		t.Fatalf("повторный AttachPermission (идемпотентность): %v", err)
	}

	// Detach + идемпотентный повтор.
	perms, err = repo.DetachPermission(ctx, role, action, resource)
	if err != nil {
		t.Fatalf("DetachPermission: %v", err)
	}
	if containsPerm(perms, action, resource) {
		t.Fatalf("ожидали отсутствие права в наборе роли после detach, получили %+v", perms)
	}
	if _, err := repo.DetachPermission(ctx, role, action, resource); err != nil {
		t.Fatalf("повторный DetachPermission (идемпотентность): %v", err)
	}

	// Системная роль: attach/detach → ErrPrecondition.
	if _, err := pool.Exec(ctx, `INSERT INTO roles (name, system) VALUES ($1, true)`, sysRole); err != nil {
		t.Fatalf("вставка системной роли: %v", err)
	}
	if _, err := repo.AttachPermission(ctx, sysRole, action, resource); !errors.Is(err, errs.ErrPrecondition) {
		t.Fatalf("ожидали ErrPrecondition (attach на системную роль), получили %v", err)
	}
	if _, err := repo.DetachPermission(ctx, sysRole, action, resource); !errors.Is(err, errs.ErrPrecondition) {
		t.Fatalf("ожидали ErrPrecondition (detach на системную роль), получили %v", err)
	}
}

// TestIntegrationDeleteRoleCascade проверяет каскадное снятие роли у носителей и
// её связок прав при удалении пользовательской роли «в использовании».
func TestIntegrationDeleteRoleCascade(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	repo := New(pool)
	ctx := context.Background()
	const role = "it-cascade-role"
	const action, resource = "it-cascade-act", "it-cascade-res"
	const subject = "it-cascade-subject"

	_, _ = pool.Exec(ctx, `DELETE FROM roles WHERE name=$1`, role)
	_, _ = pool.Exec(ctx, `DELETE FROM permissions WHERE action=$1 AND resource=$2`, action, resource)
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM subject_roles WHERE subject=$1`, subject)
		_, _ = pool.Exec(ctx, `DELETE FROM roles WHERE name=$1`, role)
		_, _ = pool.Exec(ctx, `DELETE FROM permissions WHERE action=$1 AND resource=$2`, action, resource)
	}()

	if _, err := repo.CreateRole(ctx, role); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if _, err := repo.CreatePermission(ctx, action, resource); err != nil {
		t.Fatalf("CreatePermission: %v", err)
	}
	if _, err := repo.AttachPermission(ctx, role, action, resource); err != nil {
		t.Fatalf("AttachPermission: %v", err)
	}
	if err := repo.AssignRole(ctx, subject, role); err != nil {
		t.Fatalf("AssignRole: %v", err)
	}
	// Носитель имеет право через роль.
	if allowed, err := repo.Allowed(ctx, subject, resource, action); err != nil || !allowed {
		t.Fatalf("ожидали allow до удаления роли, got=%v err=%v", allowed, err)
	}

	// Удаление роли каскадно снимает её у носителя.
	if err := repo.DeleteRole(ctx, role); err != nil {
		t.Fatalf("DeleteRole: %v", err)
	}
	if allowed, err := repo.Allowed(ctx, subject, resource, action); err != nil || allowed {
		t.Fatalf("ожидали deny после каскадного удаления роли, got=%v err=%v", allowed, err)
	}
	roles, err := repo.GetSubjectRoles(ctx, subject)
	if err != nil {
		t.Fatalf("GetSubjectRoles: %v", err)
	}
	if contains(roles, role) {
		t.Fatalf("ожидали снятие роли у носителя после каскада, получили %v", roles)
	}
}
