// Package usecase реализует доменные операции каталога сервисов поверх
// репозитория. Слой не зависит от транспорта (gRPC/HTTP) и от конкретной СУБД —
// взаимодействует со Store-интерфейсом, что позволяет подменять реализацию
// in-memory стабом в тестах (docs/IDP_MVP_plan.md, Этап 1).
package usecase

import (
	"context"
	"fmt"

	"github.com/google/uuid"

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

// WorkflowStarter запускает Temporal-workflow «Создание сервиса» (исполняется
// DevInfra worker'ом). Узкий интерфейс изолирует usecase от Temporal SDK и
// позволяет подменять его фейком в тестах.
type WorkflowStarter interface {
	StartCreateService(ctx context.Context, serviceID, project, name string) error
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
