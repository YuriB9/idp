-- Миграция каталога проектов: таблица единиц-сервисов.
-- Статусы и переходы — см. docs/adr/0004 (guarded-CAS); раскладка — docs/IDP_MVP_plan.md, БЛОК 4.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE services (
    -- id — первичный ключ записи (генерируется приложением, UUID).
    id          uuid        PRIMARY KEY,
    -- project — идентификатор проекта-владельца.
    project     text        NOT NULL,
    -- name — имя сервиса внутри проекта.
    name        text        NOT NULL,
    -- status — текстовый статус; допустимые значения ограничены CHECK,
    -- строгий маппинг в proto-enum выполняется в коде (без молчаливого дефолта).
    status      text        NOT NULL,
    -- created_at/updated_at — метки времени; created_at участвует в keyset-сортировке.
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT services_status_check
        CHECK (status IN ('creating', 'active', 'decommissioned', 'failed'))
);
-- +goose StatementEnd

-- Уникальность имени сервиса в пределах проекта (на уровне БД, не check-then-act).
-- +goose StatementBegin
CREATE UNIQUE INDEX services_project_name_uniq ON services (project, name);
-- +goose StatementEnd

-- Поддержка keyset-пагинации по (created_at, id).
-- +goose StatementBegin
CREATE INDEX services_created_at_id_idx ON services (created_at, id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE services;
-- +goose StatementEnd
