-- Признак системности ролей и прав каталога RBAC (ADR-0015, динамический каталог
-- IAM). Системные (сидированные) роли/права защищены от удаления и правки набора
-- прав через API — их случайное удаление сломало бы платформу
-- (iam-admin/owner:project:*/project-creator и их права).
--
-- Backfill: ВСЕ роли и права, существующие на момент миграции, сидированы
-- предыдущими миграциями, поэтому помечаются system=true. Создаваемые далее через
-- API роли/права получают system=false (DEFAULT). Миграция обратима.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE roles
    ADD COLUMN system boolean NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE permissions
    ADD COLUMN system boolean NOT NULL DEFAULT false;
-- +goose StatementEnd

-- Все существующие роли/права сидированы миграциями → системные.
-- +goose StatementBegin
UPDATE roles SET system = true;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE permissions SET system = true;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE permissions DROP COLUMN system;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE roles DROP COLUMN system;
-- +goose StatementEnd
