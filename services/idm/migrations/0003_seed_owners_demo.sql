-- ЛОКАЛЬНЫЕ ДЕМО-ДАННЫЕ для сценария «Изменение владельцев» (только стенд
-- docker-compose, не прод). Засевает:
--   1) права (read/list/change_owners, project:demo) и привязывает их к роли
--      project-creator → субъект demo-user может смотреть список/карточку и менять
--      владельцев в project:demo (gateway проверяет действия read/list/change_owners);
--   2) per-project роль owner:project:demo с правами (read/list/change_owners) на
--      ресурс project:demo — её выдаёт/отзывает доменный поток смены владельцев.
-- Идемпотентно (ON CONFLICT DO NOTHING) — повторный прогон безопасен.

-- +goose Up
-- +goose StatementBegin
INSERT INTO permissions (action, resource)
VALUES ('change_owners', 'project:demo'),
       ('read', 'project:demo'),
       ('list', 'project:demo')
ON CONFLICT (action, resource) DO NOTHING;
-- +goose StatementEnd

-- Права read/list/change_owners субъекту demo-user (через роль project-creator),
-- чтобы при включённом RBAC проходили чтение списка/карточки и смена владельцев.
-- +goose StatementBegin
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r, permissions p
WHERE r.name = 'project-creator'
  AND p.resource = 'project:demo'
  AND p.action IN ('read', 'list', 'change_owners')
ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- Роль владельца проекта demo.
-- +goose StatementBegin
INSERT INTO roles (name)
VALUES ('owner:project:demo')
ON CONFLICT (name) DO NOTHING;
-- +goose StatementEnd

-- Права роли владельца: read/list/change_owners над project:demo.
-- +goose StatementBegin
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r, permissions p
WHERE r.name = 'owner:project:demo'
  AND p.resource = 'project:demo'
  AND p.action IN ('read', 'list', 'change_owners')
ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM role_permissions
WHERE role_id IN (SELECT id FROM roles WHERE name = 'owner:project:demo');
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM roles WHERE name = 'owner:project:demo';
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM role_permissions
WHERE role_id IN (SELECT id FROM roles WHERE name = 'project-creator')
  AND permission_id IN (SELECT id FROM permissions WHERE action = 'change_owners' AND resource = 'project:demo');
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM subject_roles
WHERE role_id IN (SELECT id FROM roles WHERE name = 'owner:project:demo');
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM permissions
WHERE resource = 'project:demo' AND action IN ('change_owners', 'read', 'list');
-- +goose StatementEnd
