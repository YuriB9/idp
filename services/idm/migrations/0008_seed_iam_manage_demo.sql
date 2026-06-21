-- ЛОКАЛЬНЫЕ ДЕМО-ДАННЫЕ для структурного управления каталогом IAM (ADR-0015;
-- только стенд docker-compose, не прод). Засевает:
--   1) право (manage, iam:global) — привилегированное горизонтальное полномочие
--      структурных мутаций каталога (создание/удаление ролей и прав,
--      attach/detach прав роли), отдельное и привилегированнее write
--      (assign/revoke). Право системное (system=true) — защищено от удаления;
--   2) привязку права (manage, iam:global) к роли iam-admin (её уже имеет
--      demo-user из миграции 0006), чтобы раздел портала «Роли и доступы»
--      позволял структурные мутации при включённом RBAC.
-- Идемпотентно (ON CONFLICT DO NOTHING) — повторный прогон безопасен.

-- +goose Up
-- +goose StatementBegin
INSERT INTO permissions (action, resource, system)
VALUES ('manage', 'iam:global', true)
ON CONFLICT (action, resource) DO NOTHING;
-- +goose StatementEnd

-- Право manage над iam:global роли iam-admin.
-- +goose StatementBegin
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r, permissions p
WHERE r.name = 'iam-admin'
  AND p.resource = 'iam:global'
  AND p.action = 'manage'
ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM role_permissions
WHERE role_id IN (SELECT id FROM roles WHERE name = 'iam-admin')
  AND permission_id IN (SELECT id FROM permissions WHERE resource = 'iam:global' AND action = 'manage');
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM permissions
WHERE resource = 'iam:global' AND action = 'manage';
-- +goose StatementEnd
