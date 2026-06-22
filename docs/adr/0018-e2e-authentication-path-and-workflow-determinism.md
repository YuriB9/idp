# ADR-0018: Путь аутентификации сквозных E2E и модель детерминизма воркфлоу

**Status:** Accepted
**Date:** 2026-06-22
**Change:** e2e-portal-user-stories

## Context

Тест-слой плана (docs/IDP_MVP_plan.md, БЛОК 7 и раздел «Тестирование (сквозное, со
2-го этапа)») требует прогонять все 4 user stories end-to-end через портал-периметр
на docker-compose-стенде с РЕАЛЬНЫМИ Keycloak + Oauth2-Proxy — до начала работ по
Kubernetes (Этап 5). Сейчас `tests/e2e` — заглушка (`TestPerimeterHealthz` бьёт
`/healthz`). Прогон E2E задуман ЛОКАЛЬНЫМ, ручным (`docker compose` + Makefile-цель);
подъём стенда в CI вне границ этого решения.

Воркфлоу (create/change-owners/decommission/transfer), периметр REST (ADR-0009),
RBAC (ADR-0010/0014-0016), портал (ADR-0017) уже реализованы и заархивированы.
Локальный стенд (`deploy/compose/docker-compose.yml`) для удобства разработки
запускает gateway с `AUTH_DISABLED=true` и `AUTH_DISABLED_SUBJECT` = `sub`
пользователя `dev`; oauth2-proxy (:4180, provider `keycloak-oidc`, upstream
gateway:8080) присутствует, но портал ходит в gateway напрямую. Realm `idp`
содержит клиент `idp-portal` (`directAccessGrantsEnabled=true`,
secret `idp-portal-secret`) и пользователей `dev/alice/bob`.

Нужно зафиксировать два сквозных решения тест-слоя, не меняя контракт периметра,
proto и поведение бэкенда: (1) путь аутентификации E2E; (2) как детерминированно
доводить асинхронные Temporal-воркфлоу до терминального статуса и ассертить
переходы без флакелов. Опирается на ADR-0001 (Temporal), ADR-0003 (auth/
fail-closed), ADR-0004 (guarded-CAS), ADR-0005 (Saga-откат создания), ADR-0009
(периметр REST), ADR-0011 (владельцы/role-sync), ADR-0012 (decommission/
load-check), ADR-0013 (transfer PONR/dual-auth).

## Decision

- **Аутентификация E2E — реальный OIDC через oauth2-proxy, не обход AUTH_DISABLED.**
  E2E получает токен у Keycloak программно (password-grant: `grant_type=password`,
  `client_id=idp-portal`, `client_secret=idp-portal-secret`, `username/password` из
  `dev/alice/bob`) и шлёт запросы периметра на oauth2-proxy (:4180) с заголовком
  `Authorization: Bearer <access_token>`. oauth2-proxy (`pass_authorization_header`/
  `set_authorization_header`, `skip_provider_button`) проксирует на gateway. В
  E2E-режиме gateway работает с ВКЛЮЧЁННОЙ проверкой JWT (issuer/audience/JWKS), а
  не с `AUTH_DISABLED`. Это выполняет требование плана «реальный Keycloak/
  Oauth2-Proxy», проверяет OIDC-цепочку и связку `sub`→RBAC, и даёт разные субъекты
  (`dev`/`alice`/`bob`) для сценариев прав (403). Password-grant выбран против
  эмуляции browser-login (cookie-сессия): он детерминированнее и не требует
  HTML-парсинга.

- **Совместимость с локалкой — через отдельный compose-override, без правки базового
  файла.** E2E-режим (gateway с `JWKS_URL`, без `AUTH_DISABLED`; тесты на :4180)
  включается отдельным `docker-compose.e2e.yml`. Базовый `docker compose up`
  остаётся с `AUTH_DISABLED=true` и прямым ходом портала в gateway; существующий
  `conformance`-таргет (GET-only на :8081) не затрагивается. Новых секретов сверх
  realm-фикстуры не вводится.

- **Детерминизм воркфлоу — ретраи-поллинг getService с таймаут-бюджетом, не sleep.**
  Мутации периметра асинхронны (запускают workflow, отвечают 201/200 со статусом
  `creating`/`transferring`). E2E ожидает терминальный статус поллингом `getService`
  с интервалом ~500ms и конечным бюджетом (create/transfer ~60s, decommission/
  change-owners ~30s; настраивается env). Терминальный `failed` при ожидании
  `active` немедленно завершает сценарий с диагностикой (последний статус, истёкшее
  время). Переходы статусов трактуются как guarded-CAS с ожидаемым исходным статусом
  (ADR-0004). RetryPolicy/таймауты activity не меняются — E2E их только наблюдает.

- **Готовность стенда — health-gate перед тестами.** До первого сценария проверяется
  доступность токен-эндпоинта Keycloak, gateway `/healthz` и здоровье Temporal;
  тесты не стартуют, пока стенд не готов (учитывая медленный старт Keycloak ~40s и
  Temporal auto-setup ~30-60s).

- **Изоляция — уникальные имена сервисов; без зависимости от purge.** Каждый
  сценарий генерирует уникальное имя → различный детерминированный `WorkflowID`,
  безопасный `t.Parallel()` на уровне story. Идемпотентность проверяется намеренными
  повторами (create→409, decommission/transfer→200), а не очисткой. Стенд
  поднимается чистым на каждый локальный прогон (`docker compose down -v` в Makefile-цели).

- **decommission-предусловие — чек-флаг load_drained (ADR-0012).** При отсутствии
  K8s-worker в MVP предусловие снятой нагрузки моделируется явным флагом
  `load_drained` в теле `decommissionService` (контракт уже это содержит): E2E
  ассертит happy-path при `true` и 422 при `false`/неподходящем статусе. Прямой
  запрос к кластеру в MVP не выполняется. Это закрывает открытый вопрос ADR-0012 на
  уровне тест-слоя ссылкой, без изменения контракта.

- **Сценарии отказа — наблюдение Saga/PONR через моки, без изменения контракта.**
  Saga-откат создания (ADR-0005) моделируется мок-маппингом, возвращающим
  non-retryable ошибку на шаге Vault для маркированного имени сервиса → ассерт
  `failed` + alert в логах, без молчаливого перехода в `active`. PONR переноса
  (ADR-0013) наблюдается через статус/логи. Провальные сценарии при риске флака
  допустимо выносить в опциональные (env-gated), оставляя happy-path обязательными.

## Consequences

- E2E проверяет реальный периметр аутентификации и RBAC, а не его обход; стоимость —
  двойной режим gateway (изолирован в override-файл) и ручной локальный прогон через
  Makefile-цель (подъём стенда с health-gate + очистка). В CI стенд не поднимается.
- Ожидание статусов ретраями вместо sleep снижает флакелы и делает диагностику
  читаемой; цена — конечные таймаут-бюджеты, которые нужно держать щедрыми из-за
  медленного старта Keycloak/Temporal.
- Контракт периметра, proto, сгенерированный код, RBAC, миграции и бизнес-логика
  воркфлоу/activity НЕ меняются (`gen:check` остаётся зелёным); change аддитивен и
  откатывается revert'ом ветки.
