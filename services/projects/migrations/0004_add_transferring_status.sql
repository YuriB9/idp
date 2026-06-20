-- Миграция каталога проектов: транзитный статус переноса сервиса.
-- Расширяет CHECK-ограничение статуса значением 'transferring' (ADR-0013).
-- Статус проставляется на время переноса сервиса в другой проект (guarded-CAS
-- active→transferring) для защиты от конкурентных операций и наблюдаемости
-- незавершённого переноса. Раскладка — Этап 3 docs/IDP_MVP_plan.md
-- («Перенос сервиса»).

-- +goose Up
-- +goose StatementBegin
ALTER TABLE services DROP CONSTRAINT services_status_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE services
    ADD CONSTRAINT services_status_check
        CHECK (status IN ('creating', 'active', 'decommissioned', 'failed', 'transferring'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE services DROP CONSTRAINT services_status_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE services
    ADD CONSTRAINT services_status_check
        CHECK (status IN ('creating', 'active', 'decommissioned', 'failed'));
-- +goose StatementEnd
