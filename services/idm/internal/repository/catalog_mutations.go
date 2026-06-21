package repository

// Файл catalog_mutations.go — структурные мутации каталога RBAC для динамической
// IAM-админки (ADR-0015): создание/удаление ролей и прав, правка набора прав роли
// (attach/detach). Многошаговые записи выполняются в транзакции. Защита системных
// (сидированных) ролей/прав от удаления и правки — errs.ErrPrecondition.
// UNIQUE-конфликты (имя роли, пара action/resource) — errs.ErrConflict. Каскад
// subject_roles/role_permissions обеспечивают FK (ON DELETE CASCADE).
//
// Инвалидация кэша решений здесь НЕ выполняется — это ответственность usecase
// (широкая инвалидация поколением после успешного commit).

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/YuriB9/idp/pkg/errs"
)

// pgUniqueViolation — код ошибки Postgres «нарушение уникальности» (23505).
const pgUniqueViolation = "23505"

// isUniqueViolation сообщает, является ли ошибка нарушением UNIQUE-ограничения.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}

// CreateRole создаёт пользовательскую роль (system=false). Дубль имени →
// errs.ErrConflict (UNIQUE по name); не идемпотентно.
func (r *Repo) CreateRole(ctx context.Context, name string) (Role, error) {
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO roles (name, system) VALUES ($1, false)`, name); err != nil {
		if isUniqueViolation(err) {
			return Role{}, fmt.Errorf("repository: роль %q уже существует: %w", name, errs.ErrConflict)
		}
		return Role{}, fmt.Errorf("repository: создание роли: %w", err)
	}
	return Role{Name: name, System: false}, nil
}

// DeleteRole удаляет роль по имени. Системная роль → errs.ErrPrecondition;
// несуществующая → errs.ErrNotFound. Каскад subject_roles/role_permissions —
// через FK (ON DELETE CASCADE). Проверка системности и удаление — в одной
// транзакции (флаг system неизменяем, дополнительной блокировки не требуется).
func (r *Repo) DeleteRole(ctx context.Context, name string) error {
	return r.inTx(ctx, func(tx pgx.Tx) error {
		var system bool
		err := tx.QueryRow(ctx, `SELECT system FROM roles WHERE name = $1`, name).Scan(&system)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("repository: роль %q не найдена: %w", name, errs.ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("repository: поиск роли: %w", err)
		}
		if system {
			return fmt.Errorf("repository: роль %q системная: %w", name, errs.ErrPrecondition)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM roles WHERE name = $1`, name); err != nil {
			return fmt.Errorf("repository: удаление роли: %w", err)
		}
		return nil
	})
}

// CreatePermission создаёт пользовательское право (system=false). Дубль пары
// (action, resource) → errs.ErrConflict (UNIQUE).
func (r *Repo) CreatePermission(ctx context.Context, action, resource string) (Permission, error) {
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO permissions (action, resource, system) VALUES ($1, $2, false)`,
		action, resource); err != nil {
		if isUniqueViolation(err) {
			return Permission{}, fmt.Errorf("repository: право (%s,%s) уже существует: %w", action, resource, errs.ErrConflict)
		}
		return Permission{}, fmt.Errorf("repository: создание права: %w", err)
	}
	return Permission{Action: action, Resource: resource, System: false}, nil
}

// DeletePermission удаляет право по паре (action, resource). Системное право →
// errs.ErrPrecondition; несуществующее → errs.ErrNotFound. Каскад role_permissions
// — через FK.
func (r *Repo) DeletePermission(ctx context.Context, action, resource string) error {
	return r.inTx(ctx, func(tx pgx.Tx) error {
		var system bool
		err := tx.QueryRow(ctx,
			`SELECT system FROM permissions WHERE action = $1 AND resource = $2`,
			action, resource).Scan(&system)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("repository: право (%s,%s) не найдено: %w", action, resource, errs.ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("repository: поиск права: %w", err)
		}
		if system {
			return fmt.Errorf("repository: право (%s,%s) системное: %w", action, resource, errs.ErrPrecondition)
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM permissions WHERE action = $1 AND resource = $2`, action, resource); err != nil {
			return fmt.Errorf("repository: удаление права: %w", err)
		}
		return nil
	})
}

// AttachPermission прикрепляет существующее право к роли (идемпотентно: повтор —
// no-op через ON CONFLICT DO NOTHING). Роль/право не найдены → errs.ErrNotFound;
// системная роль → errs.ErrPrecondition (состав прав системной роли фиксирован
// сидированием). Право неявно НЕ создаётся. Возвращает актуальный набор прав роли.
func (r *Repo) AttachPermission(ctx context.Context, role, action, resource string) ([]Permission, error) {
	var perms []Permission
	err := r.inTx(ctx, func(tx pgx.Tx) error {
		roleID, system, err := lookupRole(ctx, tx, role)
		if err != nil {
			return err
		}
		if system {
			return fmt.Errorf("repository: роль %q системная: %w", role, errs.ErrPrecondition)
		}
		permID, err := lookupPermissionID(ctx, tx, action, resource)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO role_permissions (role_id, permission_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			roleID, permID); err != nil {
			return fmt.Errorf("repository: прикрепление права: %w", err)
		}
		perms, err = rolePermissionsTx(ctx, tx, roleID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return perms, nil
}

// DetachPermission открепляет право от роли (идемпотентно: открепление
// непривязанного/несуществующего права — успех). Роль не найдена →
// errs.ErrNotFound; системная роль → errs.ErrPrecondition. Возвращает актуальный
// набор прав роли.
func (r *Repo) DetachPermission(ctx context.Context, role, action, resource string) ([]Permission, error) {
	var perms []Permission
	err := r.inTx(ctx, func(tx pgx.Tx) error {
		roleID, system, err := lookupRole(ctx, tx, role)
		if err != nil {
			return err
		}
		if system {
			return fmt.Errorf("repository: роль %q системная: %w", role, errs.ErrPrecondition)
		}
		// Открепление по паре (action, resource); отсутствие связки/права — no-op.
		if _, err := tx.Exec(ctx, `
			DELETE FROM role_permissions
			WHERE role_id = $1
			  AND permission_id IN (
			      SELECT id FROM permissions WHERE action = $2 AND resource = $3
			  )`, roleID, action, resource); err != nil {
			return fmt.Errorf("repository: открепление права: %w", err)
		}
		perms, err = rolePermissionsTx(ctx, tx, roleID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return perms, nil
}

// lookupRole находит id и признак системности роли по имени. Несуществующая роль
// → errs.ErrNotFound.
func lookupRole(ctx context.Context, tx pgx.Tx, role string) (string, bool, error) {
	var (
		id     string
		system bool
	)
	err := tx.QueryRow(ctx, `SELECT id, system FROM roles WHERE name = $1`, role).Scan(&id, &system)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, fmt.Errorf("repository: роль %q не найдена: %w", role, errs.ErrNotFound)
	}
	if err != nil {
		return "", false, fmt.Errorf("repository: поиск роли: %w", err)
	}
	return id, system, nil
}

// lookupPermissionID находит id права по паре (action, resource). Несуществующее
// право → errs.ErrNotFound (право неявно не создаётся).
func lookupPermissionID(ctx context.Context, tx pgx.Tx, action, resource string) (string, error) {
	var id string
	err := tx.QueryRow(ctx,
		`SELECT id FROM permissions WHERE action = $1 AND resource = $2`, action, resource).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("repository: право (%s,%s) не найдено: %w", action, resource, errs.ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("repository: поиск права: %w", err)
	}
	return id, nil
}

// rolePermissionsTx читает актуальный набор прав роли по её id внутри транзакции.
func rolePermissionsTx(ctx context.Context, tx pgx.Tx, roleID string) ([]Permission, error) {
	rows, err := tx.Query(ctx, `
		SELECT p.action, p.resource, p.system
		FROM role_permissions rp
		JOIN permissions p ON p.id = rp.permission_id
		WHERE rp.role_id = $1
		ORDER BY p.resource, p.action`, roleID)
	if err != nil {
		return nil, fmt.Errorf("repository: права роли: %w", err)
	}
	defer rows.Close()
	return scanPermissions(rows)
}

// inTx выполняет fn в транзакции: commit при успехе, rollback при ошибке.
func (r *Repo) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("repository: начало транзакции: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repository: фиксация транзакции: %w", err)
	}
	return nil
}
