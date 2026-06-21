-- ЛОКАЛЬНЫЕ ДЕМО-ДАННЫЕ для справочника субъектов (ADR-0016; только стенд
-- docker-compose, не прод). Засевает:
--   1) право (read, iam:directory) — отдельное горизонтальное полномочие просмотра
--      РЕАЛЬНЫХ идентичностей пользователей (PII: username/email/display name) из
--      каталога Keycloak. Отдельно от (read/write/manage, iam:global) ради
--      наименьших привилегий. Право системное (system=true) — защищено от удаления;
--   2) привязку права (read, iam:directory) к роли iam-admin (её уже имеет
--      пользователь dev из миграции 0006/0009), чтобы пикер пользователей и
--      обогащение списка субъектов работали при включённом RBAC.
-- Идемпотентно (ON CONFLICT DO NOTHING) — повторный прогон безопасен.

-- +goose Up
-- +goose StatementBegin
INSERT INTO permissions (action, resource, system)
VALUES ('read', 'iam:directory', true)
ON CONFLICT (action, resource) DO NOTHING;
-- +goose StatementEnd

-- Право read над iam:directory роли iam-admin.
-- +goose StatementBegin
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r, permissions p
WHERE r.name = 'iam-admin'
  AND p.resource = 'iam:directory'
  AND p.action = 'read'
ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM role_permissions
WHERE role_id IN (SELECT id FROM roles WHERE name = 'iam-admin')
  AND permission_id IN (SELECT id FROM permissions WHERE resource = 'iam:directory' AND action = 'read');
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM permissions
WHERE resource = 'iam:directory' AND action = 'read';
-- +goose StatementEnd
