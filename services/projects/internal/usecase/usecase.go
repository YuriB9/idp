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

// Catalog — usecase каталога сервисов.
type Catalog struct {
	store Store
}

// New создаёт usecase поверх Store.
func New(store Store) *Catalog {
	return &Catalog{store: store}
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

// CreateRecord вставляет запись со статусом CREATING. БЕЗ запуска Temporal
// workflow (провизия ресурсов — отдельный change create-service-workflow).
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
