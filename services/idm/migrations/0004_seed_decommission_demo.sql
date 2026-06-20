-- ЛОКАЛЬНЫЕ ДЕМО-ДАННЫЕ для сценария «Вывод из эксплуатации» (только стенд
-- docker-compose, не прод). Засевает право (decommission, project:demo) и
-- привязывает его к роли project-creator → субъект demo-user может выводить
-- сервисы project:demo из эксплуатации при включённом RBAC (gateway и projects
-- проверяют действие decommission, ADR-0012). Также добавляет право decommission
-- роли владельца owner:project:demo (владельцы тоже могут выводить из эксплуатации).
-- Идемпотентно (ON CONFLICT DO NOTHING) — повторный прогон безопасен.

-- +goose Up
-- +goose StatementBegin
INSERT INTO permissions (action, resource)
VALUES ('decommission', 'project:demo')
ON CONFLICT (action, resource) DO NOTHING;
-- +goose StatementEnd

-- Право decommission субъекту demo-user (через роль project-creator).
-- +goose StatementBegin
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r, permissions p
WHERE r.name = 'project-creator'
  AND p.resource = 'project:demo'
  AND p.action = 'decommission'
ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- Право decommission роли владельца проекта demo.
-- +goose StatementBegin
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r, permissions p
WHERE r.name = 'owner:project:demo'
  AND p.resource = 'project:demo'
  AND p.action = 'decommission'
ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM role_permissions
WHERE permission_id IN (SELECT id FROM permissions WHERE action = 'decommission' AND resource = 'project:demo');
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM permissions
WHERE resource = 'project:demo' AND action = 'decommission';
-- +goose StatementEnd
