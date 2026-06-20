-- Миграция каталога проектов: отметка времени вывода из эксплуатации.
-- Вводит nullable-колонку decommissioned_at на services для soft-delete
-- (вывод сервиса из эксплуатации, ADR-0012). Колонка проставляется при переходе
-- ACTIVE→DECOMMISSIONED и остаётся NULL для прочих статусов. Данные каталога при
-- soft-delete сохраняются (физического удаления нет). Раскладка — Этап 3
-- docs/IDP_MVP_plan.md («Удаление сервиса»).

-- +goose Up
-- +goose StatementBegin
ALTER TABLE services
    -- decommissioned_at — момент вывода из эксплуатации; NULL для не выведенных.
    ADD COLUMN decommissioned_at timestamptz;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE services DROP COLUMN decommissioned_at;
-- +goose StatementEnd
