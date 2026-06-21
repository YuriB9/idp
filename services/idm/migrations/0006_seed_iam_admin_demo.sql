-- ЛОКАЛЬНЫЕ ДЕМО-ДАННЫЕ для сценария «Роли и доступы» (IAM-админка, ADR-0014;
-- только стенд docker-compose, не прод). Засевает:
--   1) права (read, iam:global) и (write, iam:global) — горизонтальные полномочия
--      IAM-админки (read — просмотр каталога ролей/прав/субъектов; write —
--      назначение/снятие ролей субъектам);
--   2) отдельную роль iam-admin с этими правами;
--   3) привязку роли iam-admin субъекту demo-user (совпадает с
--      AUTH_DISABLED_SUBJECT в compose), чтобы раздел портала «Роли и доступы»
--      работал при включённом RBAC.
-- Идемпотентно (ON CONFLICT DO NOTHING) — повторный прогон безопасен.

-- +goose Up
-- +goose StatementBegin
INSERT INTO permissions (action, resource)
VALUES ('read', 'iam:global'),
       ('write', 'iam:global')
ON CONFLICT (action, resource) DO NOTHING;
-- +goose StatementEnd

-- Роль администратора IAM.
-- +goose StatementBegin
INSERT INTO roles (name)
VALUES ('iam-admin')
ON CONFLICT (name) DO NOTHING;
-- +goose StatementEnd

-- Права роли iam-admin: read/write над ресурсом iam:global.
-- +goose StatementBegin
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r, permissions p
WHERE r.name = 'iam-admin'
  AND p.resource = 'iam:global'
  AND p.action IN ('read', 'write')
ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- Привязка роли iam-admin субъекту demo-user.
-- +goose StatementBegin
INSERT INTO subject_roles (subject, role_id)
SELECT 'demo-user', r.id
FROM roles r
WHERE r.name = 'iam-admin'
ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM subject_roles
WHERE role_id IN (SELECT id FROM roles WHERE name = 'iam-admin');
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM role_permissions
WHERE role_id IN (SELECT id FROM roles WHERE name = 'iam-admin');
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM roles WHERE name = 'iam-admin';
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM permissions
WHERE resource = 'iam:global' AND action IN ('read', 'write');
-- +goose StatementEnd
