// Package usecase реализует доменные операции каталога сервисов поверх
// репозитория. Слой не зависит от транспорта (gRPC/HTTP) и от конкретной СУБД —
// взаимодействует со Store-интерфейсом, что позволяет подменять реализацию
// in-memory стабом в тестах (docs/IDP_MVP_plan.md, Этап 1).
package usecase

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/YuriB9/idp/pkg/errs"
	"github.com/YuriB9/idp/services/projects/changeowners"
	"github.com/YuriB9/idp/services/projects/internal/repository"
)

// Store — зависимость usecase от слоя хранения. Реализуется repository.Repo и
// in-memory стабом в тестах.
type Store interface {
	Create(ctx context.Context, s repository.Service) error
	GetByName(ctx context.Context, project, name string) (repository.Service, error)
	List(ctx context.Context, project string, pageSize int, pageToken string) ([]repository.Service, string, error)
	TransitionStatus(ctx context.Context, id uuid.UUID, expected, next repository.Status) error
}

// WorkflowStarter запускает Temporal-workflow'ы (исполняются DevInfra worker'ом).
// Узкий интерфейс изолирует usecase от Temporal SDK и позволяет подменять его
// фейком в тестах.
type WorkflowStarter interface {
	StartCreateService(ctx context.Context, serviceID, project, name string) error
	// StartChangeOwners запускает workflow «Изменение владельцев». desired —
	// нормализованный желаемый набор, previous — текущий набор (для diff/
	// компенсаций), expectedVersion — версия для guarded-CAS каталога.
	StartChangeOwners(ctx context.Context, serviceID, project, name string, desired, previous []string, expectedVersion int64) error
	// StartDecommission запускает workflow «Вывод из эксплуатации». loadDrained —
	// явное предусловие снятой нагрузки из K8s (ADR-0012).
	StartDecommission(ctx context.Context, serviceID, project, name string, loadDrained bool) error
	// StartTransfer запускает workflow «Перенос сервиса». source/target — исходный
	// и целевой проекты; owners — текущий набор владельцев для переноса ролей IDM
	// (ADR-0013).
	StartTransfer(ctx context.Context, serviceID, source, target, name string, owners []string) error
}

// Catalog — usecase каталога сервисов.
type Catalog struct {
	store   Store
	starter WorkflowStarter
}

// Option конфигурирует Catalog.
type Option func(*Catalog)

// WithStarter подключает запуск workflow создания. Без него CreateService
// возвращает ошибку (запуск не сконфигурирован).
func WithStarter(s WorkflowStarter) Option {
	return func(c *Catalog) { c.starter = s }
}

// New создаёт usecase поверх Store. Запуск workflow подключается через WithStarter.
func New(store Store, opts ...Option) *Catalog {
	c := &Catalog{store: store}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Get возвращает запись каталога по (project, name). Отсутствие пробрасывается
// как errs.ErrNotFound (маппится транспортом в NotFound).
func (c *Catalog) Get(ctx context.Context, project, name string) (repository.Service, error) {
	return c.store.GetByName(ctx, project, name)
}

// List возвращает страницу сервисов проекта с keyset-пагинацией.
func (c *Catalog) List(ctx context.Context, project string, pageSize int, pageToken string) ([]repository.Service, string, error) {
	return c.store.List(ctx, project, pageSize, pageToken)
}

// CreateRecord вставляет запись со статусом CREATING БЕЗ запуска workflow.
// Используется как первый шаг CreateService и в сценариях, где запуск не нужен.
func (c *Catalog) CreateRecord(ctx context.Context, project, name string) (repository.Service, error) {
	s := repository.Service{
		ID:      uuid.New(),
		Project: project,
		Name:    name,
		Status:  repository.StatusCreating,
	}
	if err := c.store.Create(ctx, s); err != nil {
		return repository.Service{}, fmt.Errorf("usecase: создание записи каталога: %w", err)
	}
	return s, nil
}

// SetServiceOwners декларативно меняет владельцев сервиса: нормализует желаемый
// набор, проверяет существование записи и совпадение версии (optimistic-
// concurrency), вычисляет diff против текущего состава и запускает workflow
// «Изменение владельцев» (фактическая запись владельцев/синхронизация — в
// workflow, асинхронно). При пустом diff — идемпотентный no-op: workflow не
// стартует, возвращается текущее состояние. Возвращает желаемый набор владельцев
// (детерминированный порядок) и проектируемую версию.
func (c *Catalog) SetServiceOwners(ctx context.Context, project, name string, owners []string, expectedVersion int64) ([]string, int64, error) {
	if c.starter == nil {
		return nil, 0, fmt.Errorf("usecase: запуск workflow не сконфигурирован")
	}
	svc, err := c.store.GetByName(ctx, project, name)
	if err != nil {
		return nil, 0, fmt.Errorf("usecase: чтение сервиса для смены владельцев: %w", err)
	}
	desired := normalizeOwners(owners)
	// Optimistic-concurrency: версия из запроса должна совпадать с актуальной.
	if svc.OwnersVersion != expectedVersion {
		return nil, 0, fmt.Errorf("usecase: версия владельцев устарела (ожидалась %d, актуальна %d): %w",
			expectedVersion, svc.OwnersVersion, errs.ErrConflict)
	}
	add, remove := changeowners.Diff(svc.Owners, desired)
	if len(add) == 0 && len(remove) == 0 {
		// Idempotent no-op: состав не меняется — workflow не нужен.
		current := normalizeOwners(svc.Owners)
		return current, svc.OwnersVersion, nil
	}
	if err := c.starter.StartChangeOwners(ctx, svc.ID.String(), project, name, desired, svc.Owners, expectedVersion); err != nil {
		return nil, 0, fmt.Errorf("usecase: запуск workflow смены владельцев: %w", err)
	}
	// Проектируемое состояние: фактическая запись произойдёт в workflow.
	return desired, expectedVersion + 1, nil
}

// DecommissionService выводит сервис из эксплуатации (soft-delete): читает
// запись, проверяет допустимость исходного статуса и предусловие снятой нагрузки,
// затем запускает Temporal-workflow «Вывод из эксплуатации» (фактический отзыв
// доступов и guarded-CAS перевод статуса — в workflow, асинхронно). Семантика
// идемпотентна: уже выведенный сервис → no-op (workflow не стартует, возвращается
// текущая запись). Недопустимый исходный статус (creating/failed) или неснятая
// нагрузка → errs.ErrPrecondition; отсутствие записи → errs.ErrNotFound.
// Возвращает запись каталога как она персистирована на момент вызова.
func (c *Catalog) DecommissionService(ctx context.Context, project, name string, loadDrained bool) (repository.Service, error) {
	if c.starter == nil {
		return repository.Service{}, fmt.Errorf("usecase: запуск workflow не сконфигурирован")
	}
	svc, err := c.store.GetByName(ctx, project, name)
	if err != nil {
		return repository.Service{}, fmt.Errorf("usecase: чтение сервиса для вывода из эксплуатации: %w", err)
	}
	switch svc.Status {
	case repository.StatusDecommissioned:
		// Идемпотентный no-op: целевое состояние уже достигнуто.
		return svc, nil
	case repository.StatusCreating, repository.StatusFailed:
		return repository.Service{}, fmt.Errorf("usecase: недопустимый исходный статус %q: %w", svc.Status, errs.ErrPrecondition)
	}
	// Предусловие снятой нагрузки из K8s проверяется до любых побочных эффектов
	// (ADR-0012). Фактическая проверка-граница — в workflow (activity EnsureLoadDrained),
	// но очевидный отказ отсекаем синхронно, не стартуя workflow.
	if !loadDrained {
		return repository.Service{}, fmt.Errorf("usecase: нагрузка не снята из K8s: %w", errs.ErrPrecondition)
	}
	if err := c.starter.StartDecommission(ctx, svc.ID.String(), project, name, loadDrained); err != nil {
		return repository.Service{}, fmt.Errorf("usecase: запуск workflow вывода из эксплуатации: %w", err)
	}
	return svc, nil
}

// TransferService переносит сервис в другой проект (смена project-владельца):
// читает исходную запись, проверяет допустимость исходного статуса, затем
// запускает Temporal-workflow «Перенос» (фактическая смена project, перенос
// инфраструктуры и ролей — в workflow, асинхронно). Семантика идемпотентна: если
// сервис уже перенесён (есть активная запись (target, name), а (source, name)
// отсутствует) → no-op success с итоговой записью. Недопустимый исходный статус
// (creating/failed/decommissioned/transferring) → errs.ErrPrecondition; отсутствие
// записи → errs.ErrNotFound. Возвращает запись каталога на момент вызова.
func (c *Catalog) TransferService(ctx context.Context, project, name, target string) (repository.Service, error) {
	if c.starter == nil {
		return repository.Service{}, fmt.Errorf("usecase: запуск workflow не сконфигурирован")
	}
	svc, err := c.store.GetByName(ctx, project, name)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			// Возможно, перенос уже выполнен: ищем активную запись в target.
			if tsvc, terr := c.store.GetByName(ctx, target, name); terr == nil && tsvc.Status == repository.StatusActive {
				return tsvc, nil
			}
			return repository.Service{}, fmt.Errorf("usecase: чтение сервиса для переноса: %w", err)
		}
		return repository.Service{}, fmt.Errorf("usecase: чтение сервиса для переноса: %w", err)
	}
	if svc.Status != repository.StatusActive {
		// Переносить можно только активный сервис (transferring → перенос уже идёт;
		// creating/failed/decommissioned → недопустимый исходный статус).
		return repository.Service{}, fmt.Errorf("usecase: недопустимый исходный статус %q для переноса: %w", svc.Status, errs.ErrPrecondition)
	}
	if err := c.starter.StartTransfer(ctx, svc.ID.String(), project, target, name, svc.Owners); err != nil {
		return repository.Service{}, fmt.Errorf("usecase: запуск workflow переноса: %w", err)
	}
	return svc, nil
}

// normalizeOwners приводит набор владельцев к нормальной форме: отбрасывает
// пустые строки, убирает дубли, сортирует (детерминированный порядок).
func normalizeOwners(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, o := range in {
		if o == "" || seen[o] {
			continue
		}
		seen[o] = true
		out = append(out, o)
	}
	slices.Sort(out)
	return out
}

// CreateService фиксирует запись каталога (status=CREATING) и затем запускает
// Temporal-workflow «Создание сервиса» с детерминированным WorkflowID.
// Порядок строгий: запись фиксируется ПЕРВОЙ, и только при успешной вставке
// стартует workflow (workflow не стартует при неуспешной вставке). Если запуск
// workflow не удался, запись best-effort переводится в FAILED, чтобы не
// оставлять её «висящей» в CREATING без исполнителя.
func (c *Catalog) CreateService(ctx context.Context, project, name string) (repository.Service, error) {
	if c.starter == nil {
		return repository.Service{}, fmt.Errorf("usecase: запуск workflow не сконфигурирован")
	}
	s, err := c.CreateRecord(ctx, project, name)
	if err != nil {
		return repository.Service{}, err
	}
	if err := c.starter.StartCreateService(ctx, s.ID.String(), project, name); err != nil {
		// Best-effort: запись без исполнителя переводим в FAILED (guarded-CAS).
		if terr := c.store.TransitionStatus(ctx, s.ID, repository.StatusCreating, repository.StatusFailed); terr != nil {
			return repository.Service{}, fmt.Errorf("usecase: запуск workflow не удался (%w); перевод в FAILED не удался: %w", err, terr)
		}
		return repository.Service{}, fmt.Errorf("usecase: запуск workflow создания: %w", err)
	}
	return s, nil
}
