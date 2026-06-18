// Package catalog — публичная обёртка над доменным репозиторием каталога для
// внешних модулей (в частности, DevInfra worker'а, исполняющего финальную
// activity перехода статуса). Переиспользует guarded-CAS-переходы repository
// (ADR-0004), не дублируя SQL; внутренний пакет repository наружу не открывается.
package catalog

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/YuriB9/idp/services/projects/internal/repository"
)

// StatusStore выполняет финальные переходы статуса записи каталога.
type StatusStore struct {
	repo *repository.Repo
}

// NewStatusStore создаёт обёртку поверх пула соединений каталога проектов.
func NewStatusStore(pool *pgxpool.Pool) *StatusStore {
	return &StatusStore{repo: repository.New(pool)}
}

// Activate переводит запись CREATING→ACTIVE (guarded-CAS). При RowsAffected==0
// (статус уже не CREATING) repository возвращает errs.ErrConflict.
func (s *StatusStore) Activate(ctx context.Context, serviceID string) error {
	id, err := uuid.Parse(serviceID)
	if err != nil {
		return fmt.Errorf("catalog: некорректный service_id %q: %w", serviceID, err)
	}
	return s.repo.TransitionStatus(ctx, id, repository.StatusCreating, repository.StatusActive)
}

// Fail переводит запись CREATING→FAILED (guarded-CAS). При RowsAffected==0
// (статус уже не CREATING) repository возвращает errs.ErrConflict.
func (s *StatusStore) Fail(ctx context.Context, serviceID string) error {
	id, err := uuid.Parse(serviceID)
	if err != nil {
		return fmt.Errorf("catalog: некорректный service_id %q: %w", serviceID, err)
	}
	return s.repo.TransitionStatus(ctx, id, repository.StatusCreating, repository.StatusFailed)
}
