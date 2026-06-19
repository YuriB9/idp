//go:build integration

// Интеграционные тесты репозитория RBAC против реального PostgreSQL. Запуск:
//
//	go test -tags=integration ./internal/repository/...
//
// Требуется доступная БД; DSN берётся из IDM_TEST_DSN (по умолчанию —
// локальный postgres-idm). Схема должна быть применена миграциями
// (services/idm/migrations). При отсутствии БД тест помечается Skip.
package repository

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/YuriB9/idp/pkg/errs"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("IDM_TEST_DSN")
	if dsn == "" {
		dsn = "postgres://idm:idm@localhost:5433/idm?sslmode=disable"
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("нет доступа к БД (%v) — пропуск интеграционного теста", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("БД недоступна (%v) — пропуск интеграционного теста", err)
	}
	return pool
}

// seedChain создаёт роль, право и привязку субъекта, возвращая функцию очистки.
func seedChain(t *testing.T, pool *pgxpool.Pool, subject, action, resource string) func() {
	t.Helper()
	ctx := context.Background()
	var roleID string
	const roleName = "it-role"
	if err := pool.QueryRow(ctx,
		`INSERT INTO roles (name) VALUES ($1) ON CONFLICT (name) DO UPDATE SET name=EXCLUDED.name RETURNING id`,
		roleName).Scan(&roleID); err != nil {
		t.Fatalf("вставка роли: %v", err)
	}
	var permID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO permissions (action, resource) VALUES ($1, $2)
		 ON CONFLICT (action, resource) DO UPDATE SET action=EXCLUDED.action RETURNING id`,
		action, resource).Scan(&permID); err != nil {
		t.Fatalf("вставка права: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO role_permissions (role_id, permission_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		roleID, permID); err != nil {
		t.Fatalf("связь роль-право: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO subject_roles (subject, role_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		subject, roleID); err != nil {
		t.Fatalf("привязка субъекта: %v", err)
	}
	return func() {
		_, _ = pool.Exec(ctx, `DELETE FROM subject_roles WHERE role_id=$1`, roleID)
		_, _ = pool.Exec(ctx, `DELETE FROM role_permissions WHERE role_id=$1`, roleID)
		_, _ = pool.Exec(ctx, `DELETE FROM permissions WHERE id=$1`, permID)
		_, _ = pool.Exec(ctx, `DELETE FROM roles WHERE id=$1`, roleID)
	}
}

// TestIntegrationAllowed проверяет реальную EXISTS-цепочку: разрешение при
// наличии права, отказ при отсутствии (deny-by-default) и при несовпадении ресурса.
func TestIntegrationAllowed(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	const subject = "it-subject"
	cleanup := seedChain(t, pool, subject, "create", "project:it")
	defer cleanup()

	repo := New(pool)
	ctx := context.Background()

	tests := []struct {
		name                      string
		subject, resource, action string
		want                      bool
	}{
		{name: "право выдано — allow", subject: subject, resource: "project:it", action: "create", want: true},
		{name: "нет привязки субъекта — deny", subject: "stranger", resource: "project:it", action: "create", want: false},
		{name: "несовпадение ресурса — deny", subject: subject, resource: "project:other", action: "create", want: false},
		{name: "несовпадение действия — deny", subject: subject, resource: "project:it", action: "delete", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repo.Allowed(ctx, tt.subject, tt.resource, tt.action)
			if err != nil {
				t.Fatalf("Allowed: %v", err)
			}
			if got != tt.want {
				t.Fatalf("решение: получили %v, ожидали %v", got, tt.want)
			}
		})
	}
}

// TestIntegrationAssignRevokeRole проверяет идемпотентность выдачи/отзыва роли,
// NotFound для несуществующей роли и реальное влияние на решение Allowed.
func TestIntegrationAssignRevokeRole(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	const subject = "it-role-subject"
	// seedChain создаёт роль it-role с правом (change_owners, project:itr) — но без
	// привязки субъекта (привязку проверяем через AssignRole). Уберём посеянную
	// привязку, чтобы стартовать с чистого состояния субъекта.
	cleanup := seedChain(t, pool, "other-subject", "change_owners", "project:itr")
	defer cleanup()

	repo := New(pool)
	ctx := context.Background()

	// Несуществующая роль → ErrNotFound.
	if err := repo.AssignRole(ctx, subject, "no-such-role"); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("ожидали ErrNotFound, получили %v", err)
	}

	// Выдача роли it-role субъекту → Allowed становится true.
	if err := repo.AssignRole(ctx, subject, "it-role"); err != nil {
		t.Fatalf("AssignRole: %v", err)
	}
	// Идемпотентность: повторная выдача без ошибки.
	if err := repo.AssignRole(ctx, subject, "it-role"); err != nil {
		t.Fatalf("повторный AssignRole: %v", err)
	}
	if allowed, err := repo.Allowed(ctx, subject, "project:itr", "change_owners"); err != nil || !allowed {
		t.Fatalf("ожидали allow после выдачи роли, got=%v err=%v", allowed, err)
	}

	// Отзыв → Allowed становится false; повторный отзыв идемпотентен.
	if err := repo.RevokeRole(ctx, subject, "it-role"); err != nil {
		t.Fatalf("RevokeRole: %v", err)
	}
	if err := repo.RevokeRole(ctx, subject, "it-role"); err != nil {
		t.Fatalf("повторный RevokeRole: %v", err)
	}
	if allowed, err := repo.Allowed(ctx, subject, "project:itr", "change_owners"); err != nil || allowed {
		t.Fatalf("ожидали deny после отзыва роли, got=%v err=%v", allowed, err)
	}
	// Уборка привязки субъекта.
	_, _ = pool.Exec(ctx, `DELETE FROM subject_roles WHERE subject=$1`, subject)
}
