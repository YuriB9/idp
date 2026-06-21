//go:build integration

// Интеграционные тесты read-методов каталога RBAC против реального PostgreSQL.
// Запуск:
//
//	go test -tags=integration ./internal/repository/...
//
// DSN из IDM_TEST_DSN (по умолчанию — локальный postgres-idm); при отсутствии БД
// тест помечается Skip (см. testPool в repository_integration_test.go).
package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/YuriB9/idp/pkg/errs"
)

// TestIntegrationListRolesAndPermissions проверяет, что засеянные роль и право
// присутствуют в каталоге (списки не обязаны быть пустыми — БД могла быть засеяна
// миграциями, поэтому проверяем наличие, а не точный состав).
func TestIntegrationListRolesAndPermissions(t *testing.T) {
	t.Parallel()
	pool := testPool(t)
	defer pool.Close()

	cleanup := seedChain(t, pool, "cat-subject", "create", "project:cat")
	defer cleanup()

	repo := New(pool)
	ctx := context.Background()

	roles, err := repo.ListRoles(ctx)
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}
	if !containsRole(roles, "it-role") {
		t.Fatalf("ожидали роль it-role в каталоге, получили %+v", roles)
	}

	perms, err := repo.ListPermissions(ctx)
	if err != nil {
		t.Fatalf("ListPermissions: %v", err)
	}
	if !containsPerm(perms, "create", "project:cat") {
		t.Fatalf("ожидали право (create, project:cat), получили %+v", perms)
	}
}

// TestIntegrationGetRolePermissions проверяет права роли и NotFound для
// несуществующей роли.
func TestIntegrationGetRolePermissions(t *testing.T) {
	t.Parallel()
	pool := testPool(t)
	defer pool.Close()

	cleanup := seedChain(t, pool, "cat-rp-subject", "read", "project:catrp")
	defer cleanup()

	repo := New(pool)
	ctx := context.Background()

	perms, err := repo.GetRolePermissions(ctx, "it-role")
	if err != nil {
		t.Fatalf("GetRolePermissions: %v", err)
	}
	if !containsPerm(perms, "read", "project:catrp") {
		t.Fatalf("ожидали право (read, project:catrp) у роли it-role, получили %+v", perms)
	}

	if _, err := repo.GetRolePermissions(ctx, "no-such-role"); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("ожидали ErrNotFound для несуществующей роли, получили %v", err)
	}
}

// TestIntegrationListSubjectsWithRoles проверяет агрегирование ролей субъекта,
// keyset-страницы и роли субъекта; субъект без ролей не виден.
func TestIntegrationListSubjectsWithRoles(t *testing.T) {
	t.Parallel()
	pool := testPool(t)
	defer pool.Close()

	const subject = "cat-list-subject"
	cleanup := seedChain(t, pool, subject, "list", "project:catlist")
	defer cleanup()

	repo := New(pool)
	ctx := context.Background()

	// Роли конкретного субъекта.
	roles, err := repo.GetSubjectRoles(ctx, subject)
	if err != nil {
		t.Fatalf("GetSubjectRoles: %v", err)
	}
	if !contains(roles, "it-role") {
		t.Fatalf("ожидали роль it-role у субъекта, получили %v", roles)
	}

	// Субъект без ролей → пусто, без ошибки.
	empty, err := repo.GetSubjectRoles(ctx, "ghost-subject-no-roles")
	if err != nil {
		t.Fatalf("GetSubjectRoles(ghost): %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("ожидали пустой набор ролей у субъекта без ролей, получили %v", empty)
	}

	// Листинг субъектов с ролями: наш субъект должен присутствовать с ролью it-role.
	page, _, err := repo.ListSubjectsWithRoles(ctx, 0, "")
	if err != nil {
		t.Fatalf("ListSubjectsWithRoles: %v", err)
	}
	found := false
	for _, sr := range page {
		if sr.Subject == subject {
			found = true
			if !contains(sr.Roles, "it-role") {
				t.Fatalf("ожидали роль it-role у субъекта в листинге, получили %v", sr.Roles)
			}
		}
	}
	if !found {
		t.Fatalf("ожидали субъект %q в листинге субъектов", subject)
	}

	// Keyset: страница размером 1 даёт ненулевой курсор при наличии ≥2 субъектов
	// (в БД как минимум посеянный демо-субъект и наш) и страницы не пересекаются.
	first, token, err := repo.ListSubjectsWithRoles(ctx, 1, "")
	if err != nil {
		t.Fatalf("ListSubjectsWithRoles(page=1): %v", err)
	}
	if len(first) == 1 && token != "" {
		second, _, serr := repo.ListSubjectsWithRoles(ctx, 1, token)
		if serr != nil {
			t.Fatalf("ListSubjectsWithRoles(page2): %v", serr)
		}
		if len(second) == 1 && second[0].Subject <= first[0].Subject {
			t.Fatalf("keyset нарушен: вторая страница %q не строго после первой %q",
				second[0].Subject, first[0].Subject)
		}
	}

	// Повреждённый курсор → ErrValidation.
	if _, _, verr := repo.ListSubjectsWithRoles(ctx, 1, "!!!не-base64!!!"); !errors.Is(verr, errs.ErrValidation) {
		t.Fatalf("ожидали ErrValidation для повреждённого курсора, получили %v", verr)
	}
}

func containsRole(roles []Role, name string) bool {
	for _, r := range roles {
		if r.Name == name {
			return true
		}
	}
	return false
}

func containsPerm(perms []Permission, action, resource string) bool {
	for _, p := range perms {
		if p.Action == action && p.Resource == resource {
			return true
		}
	}
	return false
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
