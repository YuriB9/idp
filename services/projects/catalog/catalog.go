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

// Decommission выполняет soft-delete: guarded-CAS ACTIVE→DECOMMISSIONED с
// проставлением decommissioned_at (ADR-0012). Идемпотентен (повтор на уже
// выведенном сервисе → успех). Конкурентная смена статуса → errs.ErrConflict;
// недопустимый исходный статус → errs.ErrPrecondition; отсутствие → errs.ErrNotFound.
func (s *StatusStore) Decommission(ctx context.Context, serviceID string) error {
	id, err := uuid.Parse(serviceID)
	if err != nil {
		return fmt.Errorf("catalog: некорректный service_id %q: %w", serviceID, err)
	}
	_, err = s.repo.Decommission(ctx, id)
	return err
}

// SetOwners выполняет guarded-CAS замену набора владельцев (docs/adr/0011).
// Конфликт версии → errs.ErrConflict, отсутствие записи → errs.ErrNotFound.
func (s *StatusStore) SetOwners(ctx context.Context, serviceID string, desired []string, expectedVersion int64) error {
	id, err := uuid.Parse(serviceID)
	if err != nil {
		return fmt.Errorf("catalog: некорректный service_id %q: %w", serviceID, err)
	}
	_, _, err = s.repo.SetOwners(ctx, id, desired, expectedVersion)
	return err
}
