-- ЛОКАЛЬНЫЕ ДЕМО-ДАННЫЕ для сценария «Перенос сервиса» (только стенд
-- docker-compose, не прод). Засевает второй демо-проект project:demo2 и права
-- двусторонней авторизации переноса (ADR-0013):
--   1) право (transfer, project:demo) роли project-creator и владельцу
--      owner:project:demo → demo-user может «вынести» сервис из project:demo;
--   2) право (transfer_in, project:demo2) роли project-creator → demo-user может
--      «принять» сервис в project:demo2 (без этого права перенос в чужой проект
--      запрещён, fail-closed);
--   3) per-project роль owner:project:demo2 с правами (read/list/change_owners)
--      на project:demo2 — её выдаёт доменный поток переноса (revoke owner:demo +
--      assign owner:demo2), чтобы перенесённый сервис имел владельцев в target.
-- Идемпотентно (ON CONFLICT DO NOTHING) — повторный прогон безопасен.

-- +goose Up
-- +goose StatementBegin
INSERT INTO permissions (action, resource)
VALUES ('transfer', 'project:demo'),
       ('transfer_in', 'project:demo2'),
       ('read', 'project:demo2'),
       ('list', 'project:demo2'),
       ('change_owners', 'project:demo2')
ON CONFLICT (action, resource) DO NOTHING;
-- +goose StatementEnd

-- Права transfer (source) и transfer_in (target) субъекту demo-user через роль
-- project-creator.
-- +goose StatementBegin
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r, permissions p
WHERE r.name = 'project-creator'
  AND ((p.resource = 'project:demo' AND p.action = 'transfer')
    OR (p.resource = 'project:demo2' AND p.action = 'transfer_in'))
ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- Право transfer роли владельца проекта demo (владельцы тоже могут переносить).
-- +goose StatementBegin
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r, permissions p
WHERE r.name = 'owner:project:demo'
  AND p.resource = 'project:demo'
  AND p.action = 'transfer'
ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- Роль владельца целевого проекта demo2.
-- +goose StatementBegin
INSERT INTO roles (name)
VALUES ('owner:project:demo2')
ON CONFLICT (name) DO NOTHING;
-- +goose StatementEnd

-- Права роли владельца demo2: read/list/change_owners над project:demo2.
-- +goose StatementBegin
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r, permissions p
WHERE r.name = 'owner:project:demo2'
  AND p.resource = 'project:demo2'
  AND p.action IN ('read', 'list', 'change_owners')
ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- Чтобы demo-user видел перенесённый сервис в project:demo2 (чтение/список) при
-- включённом RBAC — выдаём read/list на project:demo2 роли project-creator.
-- +goose StatementBegin
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r, permissions p
WHERE r.name = 'project-creator'
  AND p.resource = 'project:demo2'
  AND p.action IN ('read', 'list')
ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM role_permissions
WHERE role_id IN (SELECT id FROM roles WHERE name = 'owner:project:demo2');
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM roles WHERE name = 'owner:project:demo2';
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM role_permissions
WHERE permission_id IN (
    SELECT id FROM permissions
    WHERE (resource = 'project:demo' AND action = 'transfer')
       OR (resource = 'project:demo2' AND action IN ('transfer_in', 'read', 'list', 'change_owners'))
);
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM permissions
WHERE (resource = 'project:demo' AND action = 'transfer')
   OR (resource = 'project:demo2' AND action IN ('transfer_in', 'read', 'list', 'change_owners'));
-- +goose StatementEnd
