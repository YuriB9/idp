// Package repository реализует доступ к каталогу сервисов в PostgreSQL поверх
// pgx. Переходы статусов выполняются через guarded-CAS (docs/adr/0004), а не
// check-then-act; многошаговые записи — через WithTx; публикация статусов/событий
// выполняется вызывающим кодом только после commit.
package repository

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Status — доменный статус записи каталога. Значения совпадают с CHECK-ограничением
// в миграции и со строгим маппингом в proto-enum (см. слой grpcapi).
type Status string

const (
	// StatusCreating — запись создана, провизия ещё не завершена.
	StatusCreating Status = "creating"
	// StatusActive — сервис активен.
	StatusActive Status = "active"
	// StatusDecommissioned — сервис выведен из эксплуатации (данные сохраняются).
	StatusDecommissioned Status = "decommissioned"
	// StatusFailed — провизия завершилась неустранимой ошибкой.
	StatusFailed Status = "failed"
)

// ParseStatus переводит строку из БД в доменный Status. Незнакомое значение —
// ошибка, а не молчаливый дефолт (см. docs/IDP_MVP_plan.md, БЛОК 8–9).
func ParseStatus(raw string) (Status, error) {
	switch Status(raw) {
	case StatusCreating, StatusActive, StatusDecommissioned, StatusFailed:
		return Status(raw), nil
	default:
		return "", fmt.Errorf("repository: неизвестный статус %q", raw)
	}
}

// Service — запись каталога (единица-сервис).
type Service struct {
	ID        uuid.UUID
	Project   string
	Name      string
	Status    Status
	CreatedAt time.Time
	UpdatedAt time.Time
	// Owners — текущий набор владельцев (детерминированный порядок при чтении).
	Owners []string
	// OwnersVersion — версия набора владельцев для optimistic-concurrency
	// (guarded-CAS при смене состава, docs/adr/0011).
	OwnersVersion int64
}
