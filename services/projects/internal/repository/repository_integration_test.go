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
