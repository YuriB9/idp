-- ЛОКАЛЬНЫЕ ДЕМО-ДАННЫЕ (только для стенда docker-compose, не для прода).
-- Засевает роль "project-creator" с правом (create, project:demo) и привязывает
-- к ней демо-субъекта "demo-user" (совпадает с AUTH_DISABLED_SUBJECT в compose),
-- чтобы сквозной сценарий портала «Создание сервиса» проходил при включённом
-- RBAC. Идемпотентно (ON CONFLICT DO NOTHING) — повторный прогон безопасен.

-- +goose Up
-- +goose StatementBegin
INSERT INTO roles (name)
VALUES ('project-creator')
ON CONFLICT (name) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO permissions (action, resource)
VALUES ('create', 'project:demo')
ON CONFLICT (action, resource) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r, permissions p
WHERE r.name = 'project-creator'
  AND p.action = 'create' AND p.resource = 'project:demo'
ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO subject_roles (subject, role_id)
SELECT 'demo-user', r.id
FROM roles r
WHERE r.name = 'project-creator'
ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM subject_roles
WHERE subject = 'demo-user'
  AND role_id IN (SELECT id FROM roles WHERE name = 'project-creator');
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM role_permissions
WHERE role_id IN (SELECT id FROM roles WHERE name = 'project-creator')
  AND permission_id IN (SELECT id FROM permissions WHERE action = 'create' AND resource = 'project:demo');
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM permissions WHERE action = 'create' AND resource = 'project:demo';
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM roles WHERE name = 'project-creator';
-- +goose StatementEnd
