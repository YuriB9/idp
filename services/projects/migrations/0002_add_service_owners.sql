-- Миграция каталога проектов: модель владельцев сервиса.
-- Вводит таблицу service_owners (нормализованный набор владельцев) и колонку
-- owners_version на services для optimistic-concurrency смены владельцев
-- (guarded-CAS по версии, docs/adr/0004 и docs/adr/0011). Раскладка — Этап 3
-- docs/IDP_MVP_plan.md («Изменение владельцев»).

-- +goose Up
-- +goose StatementBegin
ALTER TABLE services
    -- owners_version — версия набора владельцев; инкрементируется guarded-CAS
    -- при каждой успешной замене состава владельцев.
    ADD COLUMN owners_version bigint NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE service_owners (
    -- service_id — ссылка на запись каталога; каскад при удалении сервиса.
    service_id uuid NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    -- owner — идентификатор субъекта-владельца (совместим с sub из JWT).
    owner      text NOT NULL,
    -- Владелец атомарен по паре (service_id, owner) — дубликаты запрещены на БД.
    PRIMARY KEY (service_id, owner)
);
-- +goose StatementEnd

-- Индекс ускоряет батч-загрузку владельцев для страницы листинга.
-- +goose StatementBegin
CREATE INDEX service_owners_service_id_idx ON service_owners (service_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE service_owners;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE services DROP COLUMN owners_version;
-- +goose StatementEnd
