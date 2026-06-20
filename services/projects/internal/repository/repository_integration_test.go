//go:build integration

// Интеграционные тесты репозитория против реального PostgreSQL. Запуск:
//
//	go test -tags=integration ./internal/repository/...
//
// Требуется доступная БД; DSN берётся из PROJECTS_TEST_DSN (по умолчанию —
// локальный postgres-projects). Схема должна быть применена миграциями
// (make migrate-projects). При отсутствии БД тест помечается Skip.
package repository

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/YuriB9/idp/pkg/errs"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("PROJECTS_TEST_DSN")
	if dsn == "" {
		dsn = "postgres://projects:projects@localhost:5432/projects?sslmode=disable"
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

// cleanupProject удаляет записи проекта, чтобы тесты не мешали друг другу.
func cleanupProject(t *testing.T, pool *pgxpool.Pool, project string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `DELETE FROM services WHERE project = $1`, project); err != nil {
		t.Fatalf("очистка проекта %q: %v", project, err)
	}
}

// TestIntegrationUniqueName проверяет, что дубль (project, name) отклоняется БД
// как ErrConflict.
func TestIntegrationUniqueName(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	repo := New(pool)
	ctx := context.Background()
	const project = "it-unique"
	cleanupProject(t, pool, project)
	defer cleanupProject(t, pool, project)

	first := Service{ID: uuid.New(), Project: project, Name: "svc", Status: StatusCreating}
	if err := repo.Create(ctx, first); err != nil {
		t.Fatalf("первая вставка: %v", err)
	}
	dup := Service{ID: uuid.New(), Project: project, Name: "svc", Status: StatusCreating}
	if err := repo.Create(ctx, dup); !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("ожидали ErrConflict на дубле, получили %v", err)
	}
}

// TestIntegrationGuardedCAS проверяет реальный guarded-CAS: успех из ожидаемого
// статуса и конфликт при несовпадении.
func TestIntegrationGuardedCAS(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	repo := New(pool)
	ctx := context.Background()
	const project = "it-cas"
	cleanupProject(t, pool, project)
	defer cleanupProject(t, pool, project)

	id := uuid.New()
	if err := repo.Create(ctx, Service{ID: id, Project: project, Name: "svc", Status: StatusCreating}); err != nil {
		t.Fatalf("посев: %v", err)
	}

	// Несовпадение ожидаемого статуса → конфликт.
	if err := repo.TransitionStatus(ctx, id, StatusActive, StatusDecommissioned); !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("ожидали ErrConflict при неверном expected, получили %v", err)
	}
	// Корректный переход.
	if err := repo.TransitionStatus(ctx, id, StatusCreating, StatusActive); err != nil {
		t.Fatalf("ожидали успех перехода, получили %v", err)
	}
	got, err := repo.GetByName(ctx, project, "svc")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got.Status != StatusActive {
		t.Fatalf("статус = %q, ожидали active", got.Status)
	}
}

// TestIntegrationTxRollback проверяет, что ошибка внутри InTx откатывает все
// изменения транзакции (ничего не фиксируется).
func TestIntegrationTxRollback(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	repo := New(pool)
	ctx := context.Background()
	const project = "it-rollback"
	cleanupProject(t, pool, project)
	defer cleanupProject(t, pool, project)

	sentinel := errors.New("умышленный сбой второго шага")
	err := repo.InTx(ctx, func(tx *TxRepo) error {
		if cerr := tx.Create(ctx, Service{ID: uuid.New(), Project: project, Name: "svc", Status: StatusCreating}); cerr != nil {
			return cerr
		}
		// Второй шаг падает — транзакция должна откатиться целиком.
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("ожидали проброс ошибки шага, получили %v", err)
	}

	// Запись первого шага не должна сохраниться.
	if _, gerr := repo.GetByName(ctx, project, "svc"); !errors.Is(gerr, errs.ErrNotFound) {
		t.Fatalf("ожидали ErrNotFound после отката, получили %v", gerr)
	}
}

// TestIntegrationConcurrentCAS проверяет, что при двух конкурентных попытках
// одного перехода ровно одна побеждает, вторая получает ErrConflict.
func TestIntegrationConcurrentCAS(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	repo := New(pool)
	ctx := context.Background()
	const project = "it-concurrent"
	cleanupProject(t, pool, project)
	defer cleanupProject(t, pool, project)

	id := uuid.New()
	if err := repo.Create(ctx, Service{ID: id, Project: project, Name: "svc", Status: StatusCreating}); err != nil {
		t.Fatalf("посев: %v", err)
	}

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- repo.TransitionStatus(ctx, id, StatusCreating, StatusActive)
		}()
	}
	wg.Wait()
	close(results)

	var ok, conflicts int
	for err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, errs.ErrConflict):
			conflicts++
		default:
			t.Fatalf("неожиданная ошибка: %v", err)
		}
	}
	if ok != 1 || conflicts != 1 {
		t.Fatalf("ожидали 1 успех и 1 конфликт, получили ok=%d conflicts=%d", ok, conflicts)
	}
}

// TestIntegrationKeyset проверяет keyset-пагинацию против реальной БД: порядок,
// отсутствие дублей/пропусков и пустой курсор в конце.
func TestIntegrationKeyset(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	repo := New(pool)
	ctx := context.Background()
	const project = "it-keyset"
	cleanupProject(t, pool, project)
	defer cleanupProject(t, pool, project)

	const total = 5
	for i := range total {
		s := Service{ID: uuid.New(), Project: project, Name: "svc-" + string(rune('a'+i)), Status: StatusCreating}
		if err := repo.Create(ctx, s); err != nil {
			t.Fatalf("посев %d: %v", i, err)
		}
	}

	seen := make(map[string]int)
	token := ""
	for pages := 0; ; pages++ {
		items, next, err := repo.List(ctx, project, 2, token)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		for _, it := range items {
			seen[it.Name]++
		}
		if next == "" {
			break
		}
		token = next
		if pages > total+2 {
			t.Fatal("бесконечный цикл пагинации")
		}
	}
	if len(seen) != total {
		t.Fatalf("уникальных %d, ожидали %d", len(seen), total)
	}
	for name, cnt := range seen {
		if cnt != 1 {
			t.Fatalf("%q встретилась %d раз (ожидали 1)", name, cnt)
		}
	}

	// Повреждённый курсор → ErrValidation.
	if _, _, err := repo.List(ctx, project, 2, "!!!битый!!!"); !errors.Is(err, errs.ErrValidation) {
		t.Fatalf("ожидали ErrValidation, получили %v", err)
	}
}

// TestIntegrationSetOwners проверяет замену владельцев: применение diff,
// инкремент версии, отражение в Get, идемпотентность и guarded-CAS-конфликт по
// устаревшей версии.
func TestIntegrationSetOwners(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	repo := New(pool)
	ctx := context.Background()
	const project = "it-owners"
	cleanupProject(t, pool, project)
	defer cleanupProject(t, pool, project)

	id := uuid.New()
	if err := repo.Create(ctx, Service{ID: id, Project: project, Name: "svc", Status: StatusActive}); err != nil {
		t.Fatalf("посев: %v", err)
	}

	// Замена набора с версии 0 → {alice, bob}, версия становится 1.
	owners, ver, err := repo.SetOwners(ctx, id, []string{"bob", "alice"}, 0)
	if err != nil {
		t.Fatalf("SetOwners: %v", err)
	}
	if ver != 1 || len(owners) != 2 || owners[0] != "alice" || owners[1] != "bob" {
		t.Fatalf("ожидали [alice bob] v1, получили %v v%d", owners, ver)
	}

	// Отражение в Get: owners и версия.
	got, err := repo.GetByName(ctx, project, "svc")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got.OwnersVersion != 1 || len(got.Owners) != 2 || got.Owners[0] != "alice" || got.Owners[1] != "bob" {
		t.Fatalf("Get: owners=%v ver=%d", got.Owners, got.OwnersVersion)
	}

	// Конфликт guarded-CAS: устаревшая версия 0.
	if _, _, cerr := repo.SetOwners(ctx, id, []string{"carol"}, 0); !errors.Is(cerr, errs.ErrConflict) {
		t.Fatalf("ожидали ErrConflict на устаревшей версии, получили %v", cerr)
	}

	// Идемпотентная замена тем же набором с актуальной версией 1 → версия 2,
	// состав не меняется.
	owners2, ver2, err := repo.SetOwners(ctx, id, []string{"alice", "bob"}, 1)
	if err != nil {
		t.Fatalf("идемпотентный SetOwners: %v", err)
	}
	if ver2 != 2 || len(owners2) != 2 {
		t.Fatalf("ожидали [alice bob] v2, получили %v v%d", owners2, ver2)
	}

	// Несуществующий сервис → ErrNotFound.
	if _, _, nerr := repo.SetOwners(ctx, uuid.New(), []string{"x"}, 0); !errors.Is(nerr, errs.ErrNotFound) {
		t.Fatalf("ожидали ErrNotFound, получили %v", nerr)
	}
}

// TestIntegrationDecommission проверяет soft-delete через guarded-CAS: успешный
// перевод ACTIVE→DECOMMISSIONED с проставлением decommissioned_at и сохранением
// данных/владельцев, идемпотентный повтор, отказ-предусловие из creating и
// NotFound для отсутствующего сервиса.
func TestIntegrationDecommission(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	repo := New(pool)
	ctx := context.Background()
	const project = "it-decomm"
	cleanupProject(t, pool, project)
	defer cleanupProject(t, pool, project)

	// Активный сервис с владельцем.
	id := uuid.New()
	if err := repo.Create(ctx, Service{ID: id, Project: project, Name: "svc", Status: StatusCreating}); err != nil {
		t.Fatalf("вставка: %v", err)
	}
	if err := repo.TransitionStatus(ctx, id, StatusCreating, StatusActive); err != nil {
		t.Fatalf("перевод в active: %v", err)
	}
	if _, _, err := repo.SetOwners(ctx, id, []string{"alice"}, 0); err != nil {
		t.Fatalf("посев владельца: %v", err)
	}

	// Успешный soft-delete: статус и decommissioned_at, данные сохраняются.
	got, err := repo.Decommission(ctx, id)
	if err != nil {
		t.Fatalf("Decommission: %v", err)
	}
	if got.Status != StatusDecommissioned || got.DecommissionedAt == nil {
		t.Fatalf("ожидали decommissioned + decommissioned_at, получили %q at=%v", got.Status, got.DecommissionedAt)
	}
	if len(got.Owners) != 1 || got.Owners[0] != "alice" {
		t.Fatalf("данные владельцев должны сохраняться, получили %v", got.Owners)
	}

	// Идемпотентный повтор: успех, статус не меняется.
	if _, err := repo.Decommission(ctx, id); err != nil {
		t.Fatalf("идемпотентный повтор: %v", err)
	}

	// Предусловие: из creating decommission недопустим.
	cid := uuid.New()
	if err := repo.Create(ctx, Service{ID: cid, Project: project, Name: "creating-svc", Status: StatusCreating}); err != nil {
		t.Fatalf("вставка creating: %v", err)
	}
	if _, err := repo.Decommission(ctx, cid); !errors.Is(err, errs.ErrPrecondition) {
		t.Fatalf("ожидали ErrPrecondition из creating, получили %v", err)
	}

	// NotFound для отсутствующего сервиса.
	if _, err := repo.Decommission(ctx, uuid.New()); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("ожидали ErrNotFound, получили %v", err)
	}
}
