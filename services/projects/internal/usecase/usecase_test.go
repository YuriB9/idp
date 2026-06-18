package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/YuriB9/idp/pkg/errs"
	"github.com/YuriB9/idp/services/projects/internal/repository"
	"github.com/YuriB9/idp/services/projects/internal/usecase"
)

// TestCreateRecord проверяет вставку со статусом CREATING и конфликт дубля.
func TestCreateRecord(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	uc := usecase.New(store)
	ctx := context.Background()

	got, err := uc.CreateRecord(ctx, "proj", "svc")
	if err != nil {
		t.Fatalf("CreateRecord: неожиданная ошибка: %v", err)
	}
	if got.Status != repository.StatusCreating {
		t.Fatalf("статус новой записи = %q, ожидали %q", got.Status, repository.StatusCreating)
	}
	if got.ID == uuid.Nil {
		t.Fatal("ожидали ненулевой id")
	}

	// Повторная вставка того же (project, name) → конфликт.
	if _, err := uc.CreateRecord(ctx, "proj", "svc"); !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("ожидали ErrConflict на дубле, получили %v", err)
	}
}

// fakeStarter — управляемый стаб WorkflowStarter.
type fakeStarter struct {
	err          error
	gotServiceID string
	gotProject   string
	gotName      string
	called       bool
}

func (f *fakeStarter) StartCreateService(_ context.Context, serviceID, project, name string) error {
	f.called = true
	f.gotServiceID, f.gotProject, f.gotName = serviceID, project, name
	return f.err
}

// TestCreateService_StartsWorkflowAfterInsert: запись фиксируется со статусом
// CREATING, затем запускается workflow с её идентификатором.
func TestCreateService_StartsWorkflowAfterInsert(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	starter := &fakeStarter{}
	uc := usecase.New(store, usecase.WithStarter(starter))
	ctx := context.Background()

	got, err := uc.CreateService(ctx, "proj", "svc")
	if err != nil {
		t.Fatalf("CreateService: неожиданная ошибка: %v", err)
	}
	if got.Status != repository.StatusCreating {
		t.Fatalf("статус = %q, ожидали CREATING", got.Status)
	}
	if !starter.called {
		t.Fatal("ожидали запуск workflow")
	}
	if starter.gotServiceID != got.ID.String() || starter.gotProject != "proj" || starter.gotName != "svc" {
		t.Fatalf("в workflow переданы неверные аргументы: %+v", starter)
	}
	// Запись действительно зафиксирована (insert до запуска).
	persisted, err := uc.Get(ctx, "proj", "svc")
	if err != nil {
		t.Fatalf("запись должна быть зафиксирована: %v", err)
	}
	if persisted.Status != repository.StatusCreating {
		t.Fatalf("persisted статус = %q, ожидали CREATING", persisted.Status)
	}
}

// TestCreateService_StartFailureMarksFailed: при ошибке запуска workflow запись
// переводится в FAILED (не остаётся «висящей» в CREATING), ошибка возвращается.
func TestCreateService_StartFailureMarksFailed(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	starter := &fakeStarter{err: errors.New("temporal недоступен")}
	uc := usecase.New(store, usecase.WithStarter(starter))
	ctx := context.Background()

	if _, err := uc.CreateService(ctx, "proj", "svc"); err == nil {
		t.Fatal("ожидали ошибку при сбое запуска workflow")
	}
	persisted, err := uc.Get(ctx, "proj", "svc")
	if err != nil {
		t.Fatalf("запись должна существовать: %v", err)
	}
	if persisted.Status != repository.StatusFailed {
		t.Fatalf("статус = %q, ожидали FAILED после сбоя запуска", persisted.Status)
	}
}

// TestCreateService_NoStarterConfigured: без сконфигурированного запуска
// CreateService возвращает ошибку (а не молча игнорирует).
func TestCreateService_NoStarterConfigured(t *testing.T) {
	t.Parallel()

	uc := usecase.New(newMemStore())
	if _, err := uc.CreateService(context.Background(), "proj", "svc"); err == nil {
		t.Fatal("ожидали ошибку при отсутствии starter")
	}
}

// TestGet проверяет чтение существующей записи и NotFound для отсутствующей.
func TestGet(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	uc := usecase.New(store)
	ctx := context.Background()

	created, err := uc.CreateRecord(ctx, "proj", "svc")
	if err != nil {
		t.Fatalf("подготовка: %v", err)
	}

	tests := []struct {
		name    string
		project string
		svc     string
		wantErr error
	}{
		{name: "существующая", project: "proj", svc: "svc"},
		{name: "отсутствующая", project: "proj", svc: "missing", wantErr: errs.ErrNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := uc.Get(ctx, tc.project, tc.svc)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("ожидали %v, получили %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("неожиданная ошибка: %v", err)
			}
			if got.ID != created.ID {
				t.Fatalf("получили id %s, ожидали %s", got.ID, created.ID)
			}
		})
	}
}

// TestTransitionGuardedCAS покрывает guarded-CAS: успех из ожидаемого статуса,
// конфликт при несовпадении и при отсутствии записи (RowsAffected==0).
func TestTransitionGuardedCAS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		seed     *repository.Status // статус посеянной записи; nil — не сеять
		expected repository.Status
		next     repository.Status
		wantErr  error
	}{
		{
			name:     "успешный переход creating→active",
			seed:     ptr(repository.StatusCreating),
			expected: repository.StatusCreating,
			next:     repository.StatusActive,
		},
		{
			name:     "конфликт: статус не совпал",
			seed:     ptr(repository.StatusActive),
			expected: repository.StatusCreating,
			next:     repository.StatusActive,
			wantErr:  errs.ErrConflict,
		},
		{
			name:     "конфликт: записи нет",
			seed:     nil,
			expected: repository.StatusCreating,
			next:     repository.StatusActive,
			wantErr:  errs.ErrConflict,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			ctx := context.Background()
			id := uuid.New()
			if tc.seed != nil {
				if err := store.Create(ctx, repository.Service{ID: id, Project: "p", Name: "n", Status: *tc.seed}); err != nil {
					t.Fatalf("посев: %v", err)
				}
			}

			err := store.TransitionStatus(ctx, id, tc.expected, tc.next)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("ожидали %v, получили %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("неожиданная ошибка: %v", err)
			}
			got, _ := store.GetByName(ctx, "p", "n")
			if got.Status != tc.next {
				t.Fatalf("статус после перехода = %q, ожидали %q", got.Status, tc.next)
			}
		})
	}
}

// TestConcurrentTransition проверяет, что из двух конкурентных попыток одного
// перехода ровно одна выигрывает, вторая получает ErrConflict.
func TestConcurrentTransition(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	ctx := context.Background()
	id := uuid.New()
	if err := store.Create(ctx, repository.Service{ID: id, Project: "p", Name: "n", Status: repository.StatusCreating}); err != nil {
		t.Fatalf("посев: %v", err)
	}

	results := make(chan error, 2)
	for range 2 {
		go func() {
			results <- store.TransitionStatus(ctx, id, repository.StatusCreating, repository.StatusActive)
		}()
	}

	var ok, conflicts int
	for range 2 {
		switch err := <-results; {
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

// TestListKeysetPagination проверяет постраничный обход без дублей и пропусков,
// пустой курсор в конце и отказ на повреждённом курсоре.
func TestListKeysetPagination(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	uc := usecase.New(store)
	ctx := context.Background()

	const total = 7
	for i := range total {
		if _, err := uc.CreateRecord(ctx, "proj", fmt.Sprintf("svc-%02d", i)); err != nil {
			t.Fatalf("посев %d: %v", i, err)
		}
	}

	// Обходим страницами по 3 и собираем имена.
	seen := make(map[string]int)
	token := ""
	pages := 0
	for {
		items, next, err := uc.List(ctx, "proj", 3, token)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		pages++
		for _, it := range items {
			seen[it.Name]++
		}
		if next == "" {
			break
		}
		token = next
		if pages > total+2 {
			t.Fatal("слишком много страниц — вероятно, бесконечный цикл")
		}
	}

	if len(seen) != total {
		t.Fatalf("уникальных записей %d, ожидали %d", len(seen), total)
	}
	for name, cnt := range seen {
		if cnt != 1 {
			t.Fatalf("запись %q встретилась %d раз (ожидали 1) — дубль/пропуск", name, cnt)
		}
	}

	// Повреждённый курсор → ErrValidation.
	if _, _, err := uc.List(ctx, "proj", 3, "!!!битый!!!"); !errors.Is(err, errs.ErrValidation) {
		t.Fatalf("ожидали ErrValidation на битом курсоре, получили %v", err)
	}
}

func ptr[T any](v T) *T { return &v }
