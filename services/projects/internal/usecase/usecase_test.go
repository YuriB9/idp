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

	// поля для StartChangeOwners
	ownersCalled   bool
	gotDesired     []string
	gotPrevious    []string
	gotExpectedVer int64

	// поля для StartDecommission
	decommCalled   bool
	gotLoadDrained bool

	// поля для StartTransfer
	transferCalled bool
	gotTarget      string
	gotOwners      []string
}

func (f *fakeStarter) StartCreateService(_ context.Context, serviceID, project, name string) error {
	f.called = true
	f.gotServiceID, f.gotProject, f.gotName = serviceID, project, name
	return f.err
}

func (f *fakeStarter) StartChangeOwners(_ context.Context, serviceID, project, name string, desired, previous []string, expectedVersion int64) error {
	f.ownersCalled = true
	f.gotServiceID, f.gotProject, f.gotName = serviceID, project, name
	f.gotDesired, f.gotPrevious, f.gotExpectedVer = desired, previous, expectedVersion
	return f.err
}

func (f *fakeStarter) StartDecommission(_ context.Context, serviceID, project, name string, loadDrained bool) error {
	f.decommCalled = true
	f.gotServiceID, f.gotProject, f.gotName = serviceID, project, name
	f.gotLoadDrained = loadDrained
	return f.err
}

func (f *fakeStarter) StartTransfer(_ context.Context, serviceID, source, target, name string, owners []string) error {
	f.transferCalled = true
	f.gotServiceID, f.gotProject, f.gotName = serviceID, source, name
	f.gotTarget, f.gotOwners = target, owners
	return f.err
}

// TestDecommissionService покрывает вывод из эксплуатации: запуск workflow для
// активного сервиса со снятой нагрузкой, идемпотентный no-op для уже выведенного,
// отказ-предусловие для недопустимого статуса и неснятой нагрузки, NotFound.
func TestDecommissionService(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		seedStatus  *repository.Status // nil — не сеять (NotFound)
		loadDrained bool
		wantStart   bool
		wantErr     error
	}{
		{name: "active+drained → запуск", seedStatus: ptr(repository.StatusActive), loadDrained: true, wantStart: true},
		{name: "уже decommissioned → no-op", seedStatus: ptr(repository.StatusDecommissioned), loadDrained: true},
		{name: "creating → предусловие", seedStatus: ptr(repository.StatusCreating), loadDrained: true, wantErr: errs.ErrPrecondition},
		{name: "failed → предусловие", seedStatus: ptr(repository.StatusFailed), loadDrained: true, wantErr: errs.ErrPrecondition},
		{name: "active без снятой нагрузки → предусловие", seedStatus: ptr(repository.StatusActive), loadDrained: false, wantErr: errs.ErrPrecondition},
		{name: "отсутствует → NotFound", seedStatus: nil, loadDrained: true, wantErr: errs.ErrNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			ctx := context.Background()
			if tc.seedStatus != nil {
				if err := store.Create(ctx, repository.Service{ID: uuid.New(), Project: "p", Name: "n", Status: *tc.seedStatus}); err != nil {
					t.Fatalf("посев: %v", err)
				}
			}
			starter := &fakeStarter{}
			uc := usecase.New(store, usecase.WithStarter(starter))

			_, err := uc.DecommissionService(ctx, "p", "n", tc.loadDrained)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("ожидали %v, получили %v", tc.wantErr, err)
				}
				if starter.decommCalled {
					t.Fatal("workflow не должен стартовать при ошибке")
				}
				return
			}
			if err != nil {
				t.Fatalf("неожиданная ошибка: %v", err)
			}
			if starter.decommCalled != tc.wantStart {
				t.Fatalf("decommCalled=%v, ожидали %v", starter.decommCalled, tc.wantStart)
			}
		})
	}
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

// TestSetServiceOwners_StartsWorkflow: при непустом diff запускается workflow
// смены владельцев с нормализованным desired, previous и версией; возвращается
// проектируемое состояние (desired + version+1).
func TestSetServiceOwners_StartsWorkflow(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	ctx := context.Background()
	id := uuid.New()
	if err := store.Create(ctx, repository.Service{ID: id, Project: "p", Name: "n", Status: repository.StatusActive, Owners: []string{"alice"}, OwnersVersion: 2}); err != nil {
		t.Fatalf("посев: %v", err)
	}
	starter := &fakeStarter{}
	uc := usecase.New(store, usecase.WithStarter(starter))

	// Передаём с дублями и в произвольном порядке — usecase нормализует.
	owners, ver, err := uc.SetServiceOwners(ctx, "p", "n", []string{"bob", "alice", "bob"}, 2)
	if err != nil {
		t.Fatalf("SetServiceOwners: %v", err)
	}
	if !starter.ownersCalled {
		t.Fatal("ожидали запуск workflow смены владельцев")
	}
	if ver != 3 || len(owners) != 2 || owners[0] != "alice" || owners[1] != "bob" {
		t.Fatalf("ответ owners=%v ver=%d, ожидали [alice bob] v3", owners, ver)
	}
	if len(starter.gotDesired) != 2 || starter.gotExpectedVer != 2 {
		t.Fatalf("в workflow переданы неверные аргументы: %+v", starter)
	}
}

// TestSetServiceOwners_VersionConflict: устаревшая версия → ErrConflict без
// запуска workflow.
func TestSetServiceOwners_VersionConflict(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	ctx := context.Background()
	id := uuid.New()
	if err := store.Create(ctx, repository.Service{ID: id, Project: "p", Name: "n", Status: repository.StatusActive, OwnersVersion: 5}); err != nil {
		t.Fatalf("посев: %v", err)
	}
	starter := &fakeStarter{}
	uc := usecase.New(store, usecase.WithStarter(starter))

	if _, _, err := uc.SetServiceOwners(ctx, "p", "n", []string{"alice"}, 4); !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("ожидали ErrConflict, получили %v", err)
	}
	if starter.ownersCalled {
		t.Fatal("workflow не должен стартовать при конфликте версии")
	}
}

// TestSetServiceOwners_NoOp: при совпадении желаемого набора с текущим workflow
// не стартует (идемпотентный no-op), версия не меняется.
func TestSetServiceOwners_NoOp(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	ctx := context.Background()
	id := uuid.New()
	if err := store.Create(ctx, repository.Service{ID: id, Project: "p", Name: "n", Status: repository.StatusActive, Owners: []string{"alice", "bob"}, OwnersVersion: 1}); err != nil {
		t.Fatalf("посев: %v", err)
	}
	starter := &fakeStarter{}
	uc := usecase.New(store, usecase.WithStarter(starter))

	owners, ver, err := uc.SetServiceOwners(ctx, "p", "n", []string{"bob", "alice"}, 1)
	if err != nil {
		t.Fatalf("SetServiceOwners: %v", err)
	}
	if starter.ownersCalled {
		t.Fatal("workflow не должен стартовать при пустом diff")
	}
	if ver != 1 || len(owners) != 2 {
		t.Fatalf("ожидали неизменную версию 1 и [alice bob], получили %v v%d", owners, ver)
	}
}

// TestSetServiceOwners_NotFound: отсутствующий сервис → ErrNotFound.
func TestSetServiceOwners_NotFound(t *testing.T) {
	t.Parallel()

	uc := usecase.New(newMemStore(), usecase.WithStarter(&fakeStarter{}))
	if _, _, err := uc.SetServiceOwners(context.Background(), "p", "missing", []string{"a"}, 0); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("ожидали ErrNotFound, получили %v", err)
	}
}

// TestTransferService_StartsWorkflow: активный сервис → запускается workflow
// переноса с source/target/owners; возвращается текущая запись.
func TestTransferService_StartsWorkflow(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	ctx := context.Background()
	id := uuid.New()
	if err := store.Create(ctx, repository.Service{ID: id, Project: "demo", Name: "svc", Status: repository.StatusActive, Owners: []string{"alice"}}); err != nil {
		t.Fatalf("посев: %v", err)
	}
	starter := &fakeStarter{}
	uc := usecase.New(store, usecase.WithStarter(starter))

	svc, err := uc.TransferService(ctx, "demo", "svc", "demo2")
	if err != nil {
		t.Fatalf("TransferService: %v", err)
	}
	if !starter.transferCalled {
		t.Fatal("ожидали запуск workflow переноса")
	}
	if starter.gotProject != "demo" || starter.gotTarget != "demo2" || starter.gotName != "svc" {
		t.Fatalf("в workflow переданы неверные аргументы: %+v", starter)
	}
	if len(starter.gotOwners) != 1 || starter.gotOwners[0] != "alice" {
		t.Fatalf("ожидали owners=[alice], получили %v", starter.gotOwners)
	}
	if svc.Project != "demo" || svc.Status != repository.StatusActive {
		t.Fatalf("ожидали исходную запись demo/active, получили %+v", svc)
	}
}

// TestTransferService_IdempotentRepeat: повтор на уже перенесённом сервисе
// (есть активная (target, name), нет (source, name)) → no-op без запуска workflow.
func TestTransferService_IdempotentRepeat(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	ctx := context.Background()
	// Сервис уже в target.
	if err := store.Create(ctx, repository.Service{ID: uuid.New(), Project: "demo2", Name: "svc", Status: repository.StatusActive}); err != nil {
		t.Fatalf("посев: %v", err)
	}
	starter := &fakeStarter{}
	uc := usecase.New(store, usecase.WithStarter(starter))

	svc, err := uc.TransferService(ctx, "demo", "svc", "demo2")
	if err != nil {
		t.Fatalf("ожидали идемпотентный успех, получили %v", err)
	}
	if starter.transferCalled {
		t.Fatal("workflow не должен стартовать для уже перенесённого сервиса")
	}
	if svc.Project != "demo2" {
		t.Fatalf("ожидали итоговую запись в demo2, получили %+v", svc)
	}
}

// TestTransferService_PreconditionStatus: недопустимый исходный статус
// (transferring/creating/...) → ErrPrecondition без запуска workflow.
func TestTransferService_PreconditionStatus(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	ctx := context.Background()
	if err := store.Create(ctx, repository.Service{ID: uuid.New(), Project: "demo", Name: "svc", Status: repository.StatusTransferring}); err != nil {
		t.Fatalf("посев: %v", err)
	}
	starter := &fakeStarter{}
	uc := usecase.New(store, usecase.WithStarter(starter))

	if _, err := uc.TransferService(ctx, "demo", "svc", "demo2"); !errors.Is(err, errs.ErrPrecondition) {
		t.Fatalf("ожидали ErrPrecondition, получили %v", err)
	}
	if starter.transferCalled {
		t.Fatal("workflow не должен стартовать при недопустимом статусе")
	}
}

// TestTransferService_NotFound: отсутствующий сервис (и в source, и в target) →
// ErrNotFound.
func TestTransferService_NotFound(t *testing.T) {
	t.Parallel()

	uc := usecase.New(newMemStore(), usecase.WithStarter(&fakeStarter{}))
	if _, err := uc.TransferService(context.Background(), "demo", "missing", "demo2"); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("ожидали ErrNotFound, получили %v", err)
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
