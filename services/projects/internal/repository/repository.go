package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/YuriB9/idp/pkg/errs"
)

const (
	// defaultPageSize — размер страницы листинга по умолчанию.
	defaultPageSize = 50
	// maxPageSize — верхний предел размера страницы (защита от тяжёлых выборок).
	maxPageSize = 200
	// uniqueViolation — код ошибки PostgreSQL для нарушения уникального ограничения.
	uniqueViolation = "23505"
)

// Querier — узкий интерфейс соединения, удовлетворяемый и *pgxpool.Pool, и pgx.Tx.
// Позволяет одному и тому же коду работать как автономно, так и внутри транзакции.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Repo — репозиторий каталога сервисов поверх пула соединений. Публичные методы
// работают автономно (каждый — отдельным соединением из пула). Для многошаговых
// записей используйте InTx.
type Repo struct {
	pool *pgxpool.Pool
}

// New создаёт репозиторий поверх пула pgx.
func New(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// Create вставляет новую запись каталога. Нарушение уникальности (project, name)
// возвращается как errs.ErrConflict.
func (r *Repo) Create(ctx context.Context, s Service) error {
	return insertService(ctx, r.pool, s)
}

// GetByName читает запись по (project, name). Отсутствие → errs.ErrNotFound.
func (r *Repo) GetByName(ctx context.Context, project, name string) (Service, error) {
	return getByName(ctx, r.pool, project, name)
}

// TransitionStatus выполняет guarded-CAS переход статуса (см. InTx-вариант для
// транзакций). При RowsAffected==0 — errs.ErrConflict.
func (r *Repo) TransitionStatus(ctx context.Context, id uuid.UUID, expected, next Status) error {
	return transition(ctx, r.pool, id, expected, next)
}

// List возвращает страницу сервисов проекта с keyset-пагинацией.
func (r *Repo) List(ctx context.Context, project string, pageSize int, pageToken string) ([]Service, string, error) {
	return listServices(ctx, r.pool, project, pageSize, pageToken)
}

// InTx выполняет fn в одной транзакции: commit при успехе, rollback при ошибке
// или панике. Публикацию статусов/событий выполнять ПОСЛЕ возврата InTx без
// ошибки (после commit), а не внутри fn.
func (r *Repo) InTx(ctx context.Context, fn func(tx *TxRepo) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("repository: begin tx: %w", err)
	}
	// rollback идемпотентен: после успешного commit вернёт ErrTxClosed, который
	// мы намеренно игнорируем.
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(&TxRepo{q: tx}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repository: commit tx: %w", err)
	}
	return nil
}

// TxRepo — те же операции каталога, но в рамках открытой транзакции. Получается
// через Repo.InTx; используется для многошаговых записей.
type TxRepo struct {
	q Querier
}

// Create — см. Repo.Create, но в транзакции.
func (t *TxRepo) Create(ctx context.Context, s Service) error {
	return insertService(ctx, t.q, s)
}

// GetByName — см. Repo.GetByName, но в транзакции.
func (t *TxRepo) GetByName(ctx context.Context, project, name string) (Service, error) {
	return getByName(ctx, t.q, project, name)
}

// TransitionStatus — guarded-CAS переход статуса в транзакции.
func (t *TxRepo) TransitionStatus(ctx context.Context, id uuid.UUID, expected, next Status) error {
	return transition(ctx, t.q, id, expected, next)
}

// --- общие реализации поверх Querier (пул или транзакция) ---

func insertService(ctx context.Context, q Querier, s Service) error {
	const query = `
		INSERT INTO services (id, project, name, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, now(), now())`
	_, err := q.Exec(ctx, query, s.ID, s.Project, s.Name, string(s.Status))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return fmt.Errorf("repository: дубликат (project, name): %w", errs.ErrConflict)
		}
		return fmt.Errorf("repository: insert service: %w", err)
	}
	return nil
}

func getByName(ctx context.Context, q Querier, project, name string) (Service, error) {
	const query = `
		SELECT id, project, name, status, created_at, updated_at
		FROM services
		WHERE project = $1 AND name = $2`
	return scanService(q.QueryRow(ctx, query, project, name))
}

// transition — guarded-CAS (docs/adr/0004): UPDATE ... WHERE id=$id AND
// status=$expected. RowsAffected==0 (нет записи или статус не совпал) →
// errs.ErrConflict. Это НЕ check-then-act.
func transition(ctx context.Context, q Querier, id uuid.UUID, expected, next Status) error {
	const query = `
		UPDATE services
		SET status = $3, updated_at = now()
		WHERE id = $1 AND status = $2`
	tag, err := q.Exec(ctx, query, id, string(expected), string(next))
	if err != nil {
		return fmt.Errorf("repository: transition status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository: guarded-CAS проигран (id=%s, ожидался %s): %w", id, expected, errs.ErrConflict)
	}
	return nil
}

func listServices(ctx context.Context, q Querier, project string, pageSize int, pageToken string) ([]Service, string, error) {
	limit := clampPageSize(pageSize)

	var (
		rows pgx.Rows
		err  error
	)
	if pageToken == "" {
		const query = `
			SELECT id, project, name, status, created_at, updated_at
			FROM services
			WHERE project = $1
			ORDER BY created_at, id
			LIMIT $2`
		// limit+1: лишняя строка сигнализирует о наличии следующей страницы.
		rows, err = q.Query(ctx, query, project, limit+1)
	} else {
		cur, derr := decodeCursor(pageToken)
		if derr != nil {
			return nil, "", fmt.Errorf("%w: %v", errs.ErrValidation, derr)
		}
		const query = `
			SELECT id, project, name, status, created_at, updated_at
			FROM services
			WHERE project = $1 AND (created_at, id) > ($2, $3)
			ORDER BY created_at, id
			LIMIT $4`
		rows, err = q.Query(ctx, query, project, cur.CreatedAt, cur.ID, limit+1)
	}
	if err != nil {
		return nil, "", fmt.Errorf("repository: list services: %w", err)
	}
	defer rows.Close()

	items := make([]Service, 0, limit+1)
	for rows.Next() {
		s, serr := scanService(rows)
		if serr != nil {
			return nil, "", serr
		}
		items = append(items, s)
	}
	if rows.Err() != nil {
		return nil, "", fmt.Errorf("repository: iterate services: %w", rows.Err())
	}

	// Если получили больше limit — есть следующая страница; курсор по последней
	// отдаваемой строке, лишнюю отбрасываем.
	var next string
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		next = encodeCursor(cursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	return items, next, nil
}

// clampPageSize приводит запрошенный размер страницы к допустимому диапазону.
func clampPageSize(n int) int {
	switch {
	case n <= 0:
		return defaultPageSize
	case n > maxPageSize:
		return maxPageSize
	default:
		return n
	}
}

// rowScanner — общий интерфейс для pgx.Row и pgx.Rows (метод Scan).
type rowScanner interface {
	Scan(dest ...any) error
}

// scanService считывает запись каталога и валидирует статус из БД.
func scanService(row rowScanner) (Service, error) {
	var (
		s         Service
		rawStatus string
	)
	if err := row.Scan(&s.ID, &s.Project, &s.Name, &rawStatus, &s.CreatedAt, &s.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Service{}, fmt.Errorf("repository: сервис не найден: %w", errs.ErrNotFound)
		}
		return Service{}, fmt.Errorf("repository: scan service: %w", err)
	}
	status, err := ParseStatus(rawStatus)
	if err != nil {
		return Service{}, err
	}
	s.Status = status
	return s, nil
}
