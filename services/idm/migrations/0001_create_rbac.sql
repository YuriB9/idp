-- Миграция модели RBAC сервиса IDM: роли, права, их связь и привязка субъектов.
-- Модель и стратегия — docs/adr/0010 (нормализованный минимальный RBAC,
-- deny-by-default), docs/IDP_MVP_plan.md Этап 1. Матчинг ресурса строгий
-- (точное совпадение строки resource, без wildcard в MVP).

-- +goose Up
-- +goose StatementBegin
CREATE TABLE roles (
    -- id — первичный ключ роли (UUID, генерируется приложением/БД).
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- name — человекочитаемое уникальное имя роли.
    name        text        NOT NULL UNIQUE,
    -- created_at — метка создания.
    created_at  timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE permissions (
    -- id — первичный ключ права.
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- action — действие (например, "create", "read", "list").
    action      text        NOT NULL,
    -- resource — целевой ресурс (например, "project:demo"); сравнение строгое.
    resource    text        NOT NULL,
    -- Право атомарно по паре (action, resource) — дубликаты запрещены на уровне БД.
    CONSTRAINT permissions_action_resource_uniq UNIQUE (action, resource)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE role_permissions (
    -- Связь many-to-many «роль ↔ право». Каскад при удалении роли/права.
    role_id       uuid NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    permission_id uuid NOT NULL REFERENCES permissions (id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE subject_roles (
    -- subject — идентификатор субъекта (sub из JWT).
    subject  text NOT NULL,
    -- role_id — назначенная роль. Каскад при удалении роли.
    role_id  uuid NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    PRIMARY KEY (subject, role_id)
);
-- +goose StatementEnd

-- Индекс ускоряет выборку ролей субъекта при проверке доступа.
-- +goose StatementBegin
CREATE INDEX subject_roles_subject_idx ON subject_roles (subject);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE subject_roles;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE role_permissions;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE permissions;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE roles;
-- +goose StatementEnd
