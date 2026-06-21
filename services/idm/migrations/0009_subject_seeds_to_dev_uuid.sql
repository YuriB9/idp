-- СВЕДЕНИЕ КАНОНИЧЕСКОГО КЛЮЧА СУБЪЕКТА (ADR-0016; только стенд docker-compose,
-- не прод). Канонический ключ субъекта RBAC — это sub из JWT (UUID Keycloak), он
-- же auth.Claims.Subject и subject_roles.subject. Ранее сиды RBAC были привязаны
-- к строке 'demo-user', а реальный пользователь Keycloak 'dev' имеет sub = UUID —
-- ключи не совпадали. Эта миграция переносит ВСЕ привязки subject_roles со строки
-- 'demo-user' на детерминированный UUID пользователя 'dev' из realm-файла
-- (deploy/keycloak/idp-realm.json) и AUTH_DISABLED_SUBJECT в compose.
-- Идемпотентно по эффекту: повторный прогон Up не находит 'demo-user' и ничего не
-- меняет.

-- +goose Up
-- +goose StatementBegin
UPDATE subject_roles
SET subject = '11111111-1111-1111-1111-111111111111'
WHERE subject = 'demo-user';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE subject_roles
SET subject = 'demo-user'
WHERE subject = '11111111-1111-1111-1111-111111111111';
-- +goose StatementEnd
