package repository

// Файл catalog.go — read-only методы каталога RBAC для IAM-админки (ADR-0014):
// перечисление ролей, прав, прав роли и привязок субъект↔роль. Только чтение
// Postgres, без побочных эффектов на кэш решений. Перечисление субъектов —
// DISTINCT subject из subject_roles (субъекты без ролей системе неизвестны).

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/YuriB9/idp/pkg/errs"
)

const (
	// defaultSubjectsPageSize — размер страницы листинга субъектов по умолчанию.
	defaultSubjectsPageSize = 50
	// maxSubjectsPageSize — верхний предел размера страницы (защита от тяжёлых выборок).
	maxSubjectsPageSize = 200
)

// Role — роль каталога (стабильный идентификатор — name; внутренний id наружу
// не отдаётся).
type Role struct {
	Name string
}

// Permission — право: пара (action, resource), сравнение строгое.
type Permission struct {
	Action   string
	Resource string
}

// SubjectRoles — субъект и имена его ролей.
type SubjectRoles struct {
	Subject string
	Roles   []string
}

// ListRoles возвращает все роли каталога, упорядоченные по имени.
func (r *Repo) ListRoles(ctx context.Context) ([]Role, error) {
	rows, err := r.pool.Query(ctx, `SELECT name FROM roles ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("repository: список ролей: %w", err)
	}
	defer rows.Close()

	roles := make([]Role, 0)
	for rows.Next() {
		var role Role
		if serr := rows.Scan(&role.Name); serr != nil {
			return nil, fmt.Errorf("repository: чтение роли: %w", serr)
		}
		roles = append(roles, role)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("repository: обход ролей: %w", rows.Err())
	}
	return roles, nil
}

// ListPermissions возвращает все права каталога, упорядоченные по (resource, action).
func (r *Repo) ListPermissions(ctx context.Context) ([]Permission, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT action, resource FROM permissions ORDER BY resource, action`)
	if err != nil {
		return nil, fmt.Errorf("repository: список прав: %w", err)
	}
	defer rows.Close()

	return scanPermissions(rows)
}

// GetRolePermissions возвращает права роли по её имени. Несуществующая роль →
// errs.ErrNotFound (отличается от роли без прав, которая даёт пустой набор).
func (r *Repo) GetRolePermissions(ctx context.Context, role string) ([]Permission, error) {
	var roleID string
	if err := r.pool.QueryRow(ctx, `SELECT id FROM roles WHERE name = $1`, role).Scan(&roleID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository: роль %q не найдена: %w", role, errs.ErrNotFound)
		}
		return nil, fmt.Errorf("repository: поиск роли: %w", err)
	}

	rows, err := r.pool.Query(ctx, `
		SELECT p.action, p.resource
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

// scanPermissions вычитывает строки (action, resource) в срез прав.
func scanPermissions(rows pgx.Rows) ([]Permission, error) {
	perms := make([]Permission, 0)
	for rows.Next() {
		var p Permission
		if serr := rows.Scan(&p.Action, &p.Resource); serr != nil {
			return nil, fmt.Errorf("repository: чтение права: %w", serr)
		}
		perms = append(perms, p)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("repository: обход прав: %w", rows.Err())
	}
	return perms, nil
}

// ListSubjectsWithRoles перечисляет субъектов (DISTINCT subject из subject_roles)
// с их ролями; keyset-пагинация по subject (ASC). Роли страницы собираются одним
// запросом (array_agg + GROUP BY), без N+1. Повреждённый курсор → errs.ErrValidation.
func (r *Repo) ListSubjectsWithRoles(ctx context.Context, pageSize int, pageToken string) ([]SubjectRoles, string, error) {
	limit := clampSubjectsPageSize(pageSize)

	var (
		rows pgx.Rows
		err  error
	)
	if pageToken == "" {
		const query = `
			SELECT sr.subject, array_agg(r.name ORDER BY r.name)
			FROM subject_roles sr
			JOIN roles r ON r.id = sr.role_id
			GROUP BY sr.subject
			ORDER BY sr.subject
			LIMIT $1`
		// limit+1: лишняя строка сигнализирует о наличии следующей страницы.
		rows, err = r.pool.Query(ctx, query, limit+1)
	} else {
		after, derr := decodeSubjectCursor(pageToken)
		if derr != nil {
			return nil, "", fmt.Errorf("%w: %v", errs.ErrValidation, derr)
		}
		const query = `
			SELECT sr.subject, array_agg(r.name ORDER BY r.name)
			FROM subject_roles sr
			JOIN roles r ON r.id = sr.role_id
			WHERE sr.subject > $1
			GROUP BY sr.subject
			ORDER BY sr.subject
			LIMIT $2`
		rows, err = r.pool.Query(ctx, query, after, limit+1)
	}
	if err != nil {
		return nil, "", fmt.Errorf("repository: список субъектов: %w", err)
	}
	defer rows.Close()

	items := make([]SubjectRoles, 0, limit+1)
	for rows.Next() {
		var sr SubjectRoles
		if serr := rows.Scan(&sr.Subject, &sr.Roles); serr != nil {
			return nil, "", fmt.Errorf("repository: чтение субъекта: %w", serr)
		}
		items = append(items, sr)
	}
	if rows.Err() != nil {
		return nil, "", fmt.Errorf("repository: обход субъектов: %w", rows.Err())
	}

	// Если получили больше limit — есть следующая страница; курсор по последнему
	// отдаваемому субъекту, лишнюю строку отбрасываем.
	var next string
	if len(items) > limit {
		items = items[:limit]
		next = encodeSubjectCursor(items[len(items)-1].Subject)
	}
	return items, next, nil
}

// GetSubjectRoles возвращает имена ролей субъекта (пустой срез, не ошибка, если
// ролей нет — реестра пользователей нет, отсутствие ролей не есть NotFound).
func (r *Repo) GetSubjectRoles(ctx context.Context, subject string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT r.name
		FROM subject_roles sr
		JOIN roles r ON r.id = sr.role_id
		WHERE sr.subject = $1
		ORDER BY r.name`, subject)
	if err != nil {
		return nil, fmt.Errorf("repository: роли субъекта: %w", err)
	}
	defer rows.Close()

	roles := make([]string, 0)
	for rows.Next() {
		var name string
		if serr := rows.Scan(&name); serr != nil {
			return nil, fmt.Errorf("repository: чтение роли субъекта: %w", serr)
		}
		roles = append(roles, name)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("repository: обход ролей субъекта: %w", rows.Err())
	}
	return roles, nil
}

// clampSubjectsPageSize приводит запрошенный размер страницы к допустимому диапазону.
func clampSubjectsPageSize(n int) int {
	switch {
	case n <= 0:
		return defaultSubjectsPageSize
	case n > maxSubjectsPageSize:
		return maxSubjectsPageSize
	default:
		return n
	}
}

// encodeSubjectCursor кодирует субъект-курсор в непрозрачную base64-строку.
func encodeSubjectCursor(subject string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(subject))
}

// decodeSubjectCursor разбирает непрозрачный курсор субъекта. Декодированное
// значение валидируется как корректная UTF-8-строка без NUL: subject уходит
// текстовым параметром в SQL, а Postgres отвергает невалидный UTF-8/NUL ошибкой
// уровня БД. Без этой проверки подделанный/битый курсор давал бы 500 вместо 400;
// возвращаем повреждённый курсор как ошибку валидации (вызывающий маппит в 400).
func decodeSubjectCursor(token string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", fmt.Errorf("repository: не удалось декодировать курсор: %w", err)
	}
	s := string(raw)
	if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
		return "", fmt.Errorf("repository: курсор содержит недопустимые байты")
	}
	return s, nil
}
