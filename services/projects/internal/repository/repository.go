package repository

import (
	"context"
	"errors"
	"fmt"
	"slices"

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

// Decommission выполняет soft-delete: guarded-CAS перевод ACTIVE→DECOMMISSIONED с
// проставлением decommissioned_at (данные сохраняются). Идемпотентен: повтор на
// уже выведенном сервисе → успех (возвращается текущая запись). Недопустимый
// исходный статус (creating/failed) → errs.ErrPrecondition; конкурентная смена
// статуса → errs.ErrConflict; отсутствие записи → errs.ErrNotFound.
func (r *Repo) Decommission(ctx context.Context, id uuid.UUID) (Service, error) {
	return decommission(ctx, r.pool, id)
}

// BeginTransfer начинает перенос: guarded-CAS ACTIVE→TRANSFERRING с проверкой
// свободы (target, name) в одной транзакции (ADR-0013). Идемпотентен: если запись
// уже перенесена (project=target, active) или уже в transferring — возвращается
// текущее состояние без изменений. Занятое (target, name) → errs.ErrConflict;
// недопустимый исходный статус (creating/failed/decommissioned) →
// errs.ErrPrecondition; конкурентная смена → errs.ErrConflict; отсутствие записи
// → errs.ErrNotFound.
func (r *Repo) BeginTransfer(ctx context.Context, id uuid.UUID, target string) (Service, error) {
	var out Service
	err := r.InTx(ctx, func(tx *TxRepo) error {
		s, terr := beginTransfer(ctx, tx.q, id, target)
		if terr != nil {
			return terr
		}
		out = s
		return nil
	})
	if err != nil {
		return Service{}, err
	}
	return out, nil
}

// CommitTransfer фиксирует перенос: guarded-CAS TRANSFERRING→ACTIVE со сменой
// project на target (ADR-0013). Идемпотентен: повтор на уже перенесённой записи
// (project=target, active) → успех. Занятое (target, name) или конкурентная смена
// статуса → errs.ErrConflict; отсутствие записи → errs.ErrNotFound.
func (r *Repo) CommitTransfer(ctx context.Context, id uuid.UUID, target string) (Service, error) {
	var out Service
	err := r.InTx(ctx, func(tx *TxRepo) error {
		s, terr := commitTransfer(ctx, tx.q, id, target)
		if terr != nil {
			return terr
		}
		out = s
		return nil
	})
	if err != nil {
		return Service{}, err
	}
	return out, nil
}

// AbortTransfer — компенсация начала переноса: guarded-CAS TRANSFERRING→ACTIVE
// (project не менялся). Идемпотентен: если запись уже active → успех. Прочие
// статусы → errs.ErrConflict; отсутствие записи → errs.ErrNotFound.
func (r *Repo) AbortTransfer(ctx context.Context, id uuid.UUID) error {
	return abortTransfer(ctx, r.pool, id)
}

// SetOwners декларативно заменяет набор владельцев сервиса в одной транзакции:
// вычисляет diff против текущего состояния, применяет DELETE/INSERT и
// guarded-CAS инкрементит owners_version (docs/adr/0011). expected — ожидаемая
// версия; при несовпадении (конкурентное изменение) → errs.ErrConflict, при
// отсутствии записи → errs.ErrNotFound. Возвращает итоговый набор владельцев
// (детерминированный порядок) и новую версию.
func (r *Repo) SetOwners(ctx context.Context, id uuid.UUID, desired []string, expected int64) ([]string, int64, error) {
	var (
		owners  []string
		version int64
	)
	err := r.InTx(ctx, func(tx *TxRepo) error {
		o, v, serr := setOwners(ctx, tx.q, id, desired, expected)
		if serr != nil {
			return serr
		}
		owners, version = o, v
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return owners, version, nil
}

// SetOwners — см. Repo.SetOwners, но в рамках уже открытой транзакции.
func (t *TxRepo) SetOwners(ctx context.Context, id uuid.UUID, desired []string, expected int64) ([]string, int64, error) {
	return setOwners(ctx, t.q, id, desired, expected)
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
		SELECT id, project, name, status, created_at, updated_at, owners_version, decommissioned_at
		FROM services
		WHERE project = $1 AND name = $2`
	s, err := scanService(q.QueryRow(ctx, query, project, name))
	if err != nil {
		return Service{}, err
	}
	owners, err := loadOwners(ctx, q, []uuid.UUID{s.ID})
	if err != nil {
		return Service{}, err
	}
	s.Owners = owners[s.ID]
	return s, nil
}

// loadOwners батч-загружает владельцев для набора сервисов одним запросом
// (без N+1). Владельцы возвращаются в детерминированном (лексикографическом)
// порядке. Сервисы без владельцев в карте отсутствуют (вызывающий трактует как
// пустой набор).
func loadOwners(ctx context.Context, q Querier, ids []uuid.UUID) (map[uuid.UUID][]string, error) {
	out := make(map[uuid.UUID][]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	const query = `
		SELECT service_id, owner
		FROM service_owners
		WHERE service_id = ANY($1)
		ORDER BY service_id, owner`
	rows, err := q.Query(ctx, query, ids)
	if err != nil {
		return nil, fmt.Errorf("repository: load owners: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id    uuid.UUID
			owner string
		)
		if serr := rows.Scan(&id, &owner); serr != nil {
			return nil, fmt.Errorf("repository: scan owner: %w", serr)
		}
		out[id] = append(out[id], owner)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("repository: iterate owners: %w", rows.Err())
	}
	return out, nil
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

// decommission — soft-delete через guarded-CAS ACTIVE→DECOMMISSIONED (docs/adr/0012):
// UPDATE ... WHERE id=$id AND status='active', проставление decommissioned_at.
// Это НЕ check-then-act: при RowsAffected==0 текущий статус перечитывается лишь
// для различения идемпотентного повтора (decommissioned → успех), недопустимого
// предусловия (creating/failed → ErrPrecondition), конкурентного конфликта
// (ErrConflict) и отсутствия записи (ErrNotFound). Данные сохраняются.
func decommission(ctx context.Context, q Querier, id uuid.UUID) (Service, error) {
	const query = `
		UPDATE services
		SET status = $2, decommissioned_at = now(), updated_at = now()
		WHERE id = $1 AND status = $3`
	tag, err := q.Exec(ctx, query, id, string(StatusDecommissioned), string(StatusActive))
	if err != nil {
		return Service{}, fmt.Errorf("repository: decommission: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Текущая запись для различения no-op/предусловия/конфликта/отсутствия.
		cur, gerr := getByIDStatus(ctx, q, id)
		if gerr != nil {
			return Service{}, gerr
		}
		switch cur.Status {
		case StatusDecommissioned:
			// Идемпотентный повтор: целевое состояние уже достигнуто.
			return getByID(ctx, q, id)
		case StatusCreating, StatusFailed:
			return Service{}, fmt.Errorf("repository: недопустимый исходный статус %q: %w", cur.Status, errs.ErrPrecondition)
		default:
			return Service{}, fmt.Errorf("repository: конкурентная смена статуса (id=%s): %w", id, errs.ErrConflict)
		}
	}
	return getByID(ctx, q, id)
}

// beginTransfer — фаза начала переноса поверх Querier (внутри транзакции):
// проверка свободы (target, name) + guarded-CAS ACTIVE→TRANSFERRING. Идемпотентен
// (см. Repo.BeginTransfer). Это НЕ check-then-act для самого перехода: статус
// перечитывается лишь для различения идемпотентного повтора, предусловия и
// конфликта.
func beginTransfer(ctx context.Context, q Querier, id uuid.UUID, target string) (Service, error) {
	cur, err := getByID(ctx, q, id)
	if err != nil {
		return Service{}, err
	}
	// Идемпотентность: перенос уже завершён или уже идёт — без повторного CAS.
	if cur.Project == target && cur.Status == StatusActive {
		return cur, nil
	}
	if cur.Status == StatusTransferring {
		return cur, nil
	}
	// Проверка свободы (target, name) до побочных эффектов.
	var exists int
	switch err := q.QueryRow(ctx,
		`SELECT 1 FROM services WHERE project = $1 AND name = $2`, target, cur.Name).Scan(&exists); {
	case err == nil:
		return Service{}, fmt.Errorf("repository: имя занято в target (%s, %s): %w", target, cur.Name, errs.ErrConflict)
	case errors.Is(err, pgx.ErrNoRows):
		// свободно — продолжаем
	default:
		return Service{}, fmt.Errorf("repository: проверка свободы имени в target: %w", err)
	}
	// guarded-CAS ACTIVE→TRANSFERRING.
	tag, err := q.Exec(ctx,
		`UPDATE services SET status = $2, updated_at = now() WHERE id = $1 AND status = $3`,
		id, string(StatusTransferring), string(StatusActive))
	if err != nil {
		return Service{}, fmt.Errorf("repository: begin transfer: %w", err)
	}
	if tag.RowsAffected() == 0 {
		switch cur.Status {
		case StatusCreating, StatusFailed, StatusDecommissioned:
			return Service{}, fmt.Errorf("repository: недопустимый исходный статус %q: %w", cur.Status, errs.ErrPrecondition)
		default:
			return Service{}, fmt.Errorf("repository: конкурентная смена статуса (id=%s): %w", id, errs.ErrConflict)
		}
	}
	return getByID(ctx, q, id)
}

// commitTransfer — фаза фиксации переноса поверх Querier: guarded-CAS
// TRANSFERRING→ACTIVE со сменой project на target. Идемпотентен (см.
// Repo.CommitTransfer). Занятое (target, name) ловится unique-violation →
// errs.ErrConflict.
func commitTransfer(ctx context.Context, q Querier, id uuid.UUID, target string) (Service, error) {
	cur, err := getByID(ctx, q, id)
	if err != nil {
		return Service{}, err
	}
	// Идемпотентность: запись уже перенесена.
	if cur.Project == target && cur.Status == StatusActive {
		return cur, nil
	}
	tag, err := q.Exec(ctx,
		`UPDATE services SET project = $2, status = $3, updated_at = now() WHERE id = $1 AND status = $4`,
		id, target, string(StatusActive), string(StatusTransferring))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return Service{}, fmt.Errorf("repository: имя занято в target (%s, %s): %w", target, cur.Name, errs.ErrConflict)
		}
		return Service{}, fmt.Errorf("repository: commit transfer: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Статус не TRANSFERRING — конкурентная смена.
		return Service{}, fmt.Errorf("repository: конкурентная смена статуса (id=%s): %w", id, errs.ErrConflict)
	}
	return getByID(ctx, q, id)
}

// abortTransfer — компенсация начала переноса поверх Querier: guarded-CAS
// TRANSFERRING→ACTIVE (project не менялся). Идемпотентен.
func abortTransfer(ctx context.Context, q Querier, id uuid.UUID) error {
	tag, err := q.Exec(ctx,
		`UPDATE services SET status = $2, updated_at = now() WHERE id = $1 AND status = $3`,
		id, string(StatusActive), string(StatusTransferring))
	if err != nil {
		return fmt.Errorf("repository: abort transfer: %w", err)
	}
	if tag.RowsAffected() == 0 {
		cur, gerr := getByIDStatus(ctx, q, id)
		if gerr != nil {
			return gerr
		}
		if cur.Status == StatusActive {
			return nil // идемпотентная компенсация: уже active
		}
		return fmt.Errorf("repository: конкурентная смена статуса при компенсации (id=%s): %w", id, errs.ErrConflict)
	}
	return nil
}

// getByIDStatus читает только статус записи по id (для разбора guarded-CAS).
// Отсутствие → errs.ErrNotFound.
func getByIDStatus(ctx context.Context, q Querier, id uuid.UUID) (Service, error) {
	var rawStatus string
	if err := q.QueryRow(ctx, `SELECT status FROM services WHERE id = $1`, id).Scan(&rawStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Service{}, fmt.Errorf("repository: сервис не найден: %w", errs.ErrNotFound)
		}
		return Service{}, fmt.Errorf("repository: чтение статуса: %w", err)
	}
	st, err := ParseStatus(rawStatus)
	if err != nil {
		return Service{}, err
	}
	return Service{ID: id, Status: st}, nil
}

// getByID читает полную запись каталога (с владельцами) по id. Отсутствие →
// errs.ErrNotFound.
func getByID(ctx context.Context, q Querier, id uuid.UUID) (Service, error) {
	const query = `
		SELECT id, project, name, status, created_at, updated_at, owners_version, decommissioned_at
		FROM services
		WHERE id = $1`
	s, err := scanService(q.QueryRow(ctx, query, id))
	if err != nil {
		return Service{}, err
	}
	owners, err := loadOwners(ctx, q, []uuid.UUID{s.ID})
	if err != nil {
		return Service{}, err
	}
	s.Owners = owners[s.ID]
	return s, nil
}

// setOwners выполняет замену набора владельцев поверх Querier (внутри транзакции).
// Порядок: проверка существования → guarded-CAS версии → применение diff.
func setOwners(ctx context.Context, q Querier, id uuid.UUID, desired []string, expected int64) ([]string, int64, error) {
	// Проверка существования записи (для различения NotFound и Conflict).
	var curVersion int64
	if err := q.QueryRow(ctx, `SELECT owners_version FROM services WHERE id = $1`, id).Scan(&curVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, fmt.Errorf("repository: сервис не найден: %w", errs.ErrNotFound)
		}
		return nil, 0, fmt.Errorf("repository: чтение версии владельцев: %w", err)
	}

	// guarded-CAS по версии (docs/adr/0004): запись существует, но версия не та →
	// конкурентное изменение → ErrConflict.
	tag, err := q.Exec(ctx,
		`UPDATE services SET owners_version = owners_version + 1, updated_at = now() WHERE id = $1 AND owners_version = $2`,
		id, expected)
	if err != nil {
		return nil, 0, fmt.Errorf("repository: guarded-CAS версии владельцев: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, 0, fmt.Errorf("repository: версия владельцев устарела (id=%s, ожидалась %d): %w", id, expected, errs.ErrConflict)
	}

	// Текущий набор владельцев → diff с желаемым.
	curOwners, err := loadOwners(ctx, q, []uuid.UUID{id})
	if err != nil {
		return nil, 0, err
	}
	current := map[string]bool{}
	for _, o := range curOwners[id] {
		current[o] = true
	}
	desiredSet := map[string]bool{}
	for _, o := range desired {
		desiredSet[o] = true
	}

	// Удаляем отозванных.
	for o := range current {
		if !desiredSet[o] {
			if _, derr := q.Exec(ctx, `DELETE FROM service_owners WHERE service_id = $1 AND owner = $2`, id, o); derr != nil {
				return nil, 0, fmt.Errorf("repository: удаление владельца: %w", derr)
			}
		}
	}
	// Добавляем новых (идемпотентно: ON CONFLICT DO NOTHING).
	for o := range desiredSet {
		if !current[o] {
			if _, ierr := q.Exec(ctx,
				`INSERT INTO service_owners (service_id, owner) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
				id, o); ierr != nil {
				return nil, 0, fmt.Errorf("repository: добавление владельца: %w", ierr)
			}
		}
	}

	// Итоговый набор — желаемый, в детерминированном порядке.
	final := append([]string(nil), desired...)
	slices.Sort(final)
	return final, expected + 1, nil
}

func listServices(ctx context.Context, q Querier, project string, pageSize int, pageToken string) ([]Service, string, error) {
	limit := clampPageSize(pageSize)

	var (
		rows pgx.Rows
		err  error
	)
	if pageToken == "" {
		const query = `
			SELECT id, project, name, status, created_at, updated_at, owners_version, decommissioned_at
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
			SELECT id, project, name, status, created_at, updated_at, owners_version, decommissioned_at
			FROM services
			WHERE project = $1 AND (created_at, id) > ($2, $3)
			ORDER BY created_at, id
			LIMIT $4`
		rows, err = q.Query(ctx, query, project, cur.CreatedAt, cur.ID, limit+1)
	}
	if err != nil {
		return nil, "", fmt.Errorf("repository: list services: %w", err)
	}

	items := make([]Service, 0, limit+1)
	for rows.Next() {
		s, serr := scanService(rows)
		if serr != nil {
			rows.Close()
			return nil, "", serr
		}
		items = append(items, s)
	}
	if rows.Err() != nil {
		rows.Close()
		return nil, "", fmt.Errorf("repository: iterate services: %w", rows.Err())
	}
	// Закрываем rows до батч-запроса владельцев: на одном соединении (pgx.Tx)
	// нельзя держать два активных запроса одновременно.
	rows.Close()

	// Если получили больше limit — есть следующая страница; курсор по последней
	// отдаваемой строке, лишнюю отбрасываем.
	var next string
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		next = encodeCursor(cursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}

	// Батч-загрузка владельцев для страницы (без N+1).
	ids := make([]uuid.UUID, len(items))
	for i := range items {
		ids[i] = items[i].ID
	}
	owners, oerr := loadOwners(ctx, q, ids)
	if oerr != nil {
		return nil, "", oerr
	}
	for i := range items {
		items[i].Owners = owners[items[i].ID]
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
	if err := row.Scan(&s.ID, &s.Project, &s.Name, &rawStatus, &s.CreatedAt, &s.UpdatedAt, &s.OwnersVersion, &s.DecommissionedAt); err != nil {
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
