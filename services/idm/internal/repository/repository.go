// Package repository — доступ к модели RBAC в Postgres (pgx). Транспорт и
// кэширование сюда не проникают: слой отвечает только за запросы к БД.
// Модель — docs/adr/0010 (роли/права/связи/привязки, deny-by-default).
package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
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

// Ping проверяет доступность БД (для content-aware /readyz).
func (r *Repo) Ping(ctx context.Context) error {
	return r.pool.Ping(ctx)
}
