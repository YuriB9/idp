// Package repository — доступ к модели RBAC в Postgres (pgx). Транспорт и
// кэширование сюда не проникают: слой отвечает только за запросы к БД.
// Модель — docs/adr/0010 (роли/права/связи/привязки, deny-by-default).
package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/YuriB9/idp/pkg/errs"
)

// Repo читает модель RBAC из Postgres.
type Repo struct {
	pool *pgxpool.Pool
}

// New создаёт репозиторий поверх пула соединений.
func New(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// Allowed возвращает true, если у субъекта есть цепочка
// subject_roles → role_permissions → permissions, дающая право (action,
// resource). Совпадение ресурса строгое (точное). Отсутствие цепочки —
// deny-by-default (false без ошибки).
func (r *Repo) Allowed(ctx context.Context, subject, resource, action string) (bool, error) {
	const q = `
SELECT EXISTS (
    SELECT 1
    FROM subject_roles sr
    JOIN role_permissions rp ON rp.role_id = sr.role_id
    JOIN permissions p ON p.id = rp.permission_id
    WHERE sr.subject = $1 AND p.resource = $2 AND p.action = $3
)`
	var allowed bool
	if err := r.pool.QueryRow(ctx, q, subject, resource, action).Scan(&allowed); err != nil {
		return false, fmt.Errorf("repository: запрос решения RBAC: %w", err)
	}
	return allowed, nil
}

// AssignRole выдаёт субъекту роль по её имени (идемпотентно: повторная выдача —
// no-op через ON CONFLICT DO NOTHING). Несуществующая роль → errs.ErrNotFound
// (без создания «висячих» привязок).
func (r *Repo) AssignRole(ctx context.Context, subject, role string) error {
	var roleID string
	if err := r.pool.QueryRow(ctx, `SELECT id FROM roles WHERE name = $1`, role).Scan(&roleID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("repository: роль %q не найдена: %w", role, errs.ErrNotFound)
		}
		return fmt.Errorf("repository: поиск роли: %w", err)
	}
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO subject_roles (subject, role_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		subject, roleID); err != nil {
		return fmt.Errorf("repository: выдача роли: %w", err)
	}
	return nil
}

// RevokeRole отзывает у субъекта роль по её имени (идемпотентно: отзыв
// отсутствующей привязки или несуществующей роли — успех, 0 затронутых строк).
func (r *Repo) RevokeRole(ctx context.Context, subject, role string) error {
	if _, err := r.pool.Exec(ctx,
		`DELETE FROM subject_roles
		 WHERE subject = $1 AND role_id IN (SELECT id FROM roles WHERE name = $2)`,
		subject, role); err != nil {
		return fmt.Errorf("repository: отзыв роли: %w", err)
	}
	return nil
}

// Ping проверяет доступность БД (для content-aware /readyz).
func (r *Repo) Ping(ctx context.Context) error {
	return r.pool.Ping(ctx)
}
