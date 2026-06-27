## 1. Подготовка

- [x] 1.1 Сверка с docs/IDP_MVP_План.md и затронутыми ADR (0017 дизайн-система, 0018 E2E,
  0004 CAS, 0005 Saga, 0001/0008 Temporal, 0011/0012/0013 owners/decommission/transfer,
  0019/0020/0021 реальные плечи), а также с новым ADR-0022 (вариант B, источник шагов)
- [x] 1.2 Создать git-ветку `change/portal-unified-workflow-progress` от master (прямые
  коммиты в master запрещены)
- [x] 1.3 Зафиксировать русские подписи шагов на операцию по порядку активностей
  `services/projects/{provisioning,changeowners,decommission,transfer}` (только справочник
  имён, код воркфлоу НЕ трогаем)

## 2. Примитив ступенчатого прогресса (дизайн-система, ADR-0017)

- [x] 2.1 Создать `web/src/components/ui/stepper.tsx` — презентационный компонент:
  пропсы `steps: { key; label; state: pending|running|done|failed }[]` + опц.
  `irreversible`; цвет+иконка на состояние (по образцу `StatusBadge`), motion-safe-спиннер
  (`motion-safe:animate-spin`), контейнер `role` + `aria-live="polite"`; без сырых ошибок
- [x] 2.2 Тест `web/src/components/ui/stepper.test.tsx` — отрисовка состояний, пометка
  точки невозврата, наличие `aria-live`/`role`, уважение reduced-motion (статичная
  индикация)

## 3. Хук поллинга статуса

- [x] 3.1 Создать `web/src/hooks/useServiceStatus.ts` — вынести логику поллинга из
  `ServiceProgressPage` (queryKey `["service", project, name]`, `refetchInterval`→`false`
  на терминале `active|failed|decommissioned`, `POLL_INTERVAL_MS=1500`, без `sleep`,
  zod `.parse` через клиент); вернуть `{ data, status, isLoading, isError }`
- [x] 3.2 Тест `web/src/hooks/useServiceStatus.test.tsx` — остановка опроса на терминале,
  продолжение на нетерминальном статусе, явное падение при дрейфе контракта

## 4. Клиентская модель шагов воркфлоу (вариант B)

- [x] 4.1 Создать `web/src/lib/workflow-steps.ts` — чистая функция
  `buildSteps(operation, status, domain) → Step[]` для create/change-owners/decommission/
  transfer: фиксированный порядок+русские подписи, вывод фазы из статуса + `owners_version`/
  `decommissioned_at`; вырожденный степпер для смены владельцев; `failed` → неуспех с
  сообщением об откате (Saga) без атрибуции шага
- [x] 4.2 Тест `web/src/lib/workflow-steps.test.ts` — таблица вход(operation,status,domain)
  → ожидаемые состояния шагов для всех операций и терминальных исходов

## 5. Рефактор страницы прогресса и формы создания

- [x] 5.1 Рефактор `web/src/pages/ServiceProgressPage.tsx` — заменить однострочную панель
  на `Stepper`, питаемый `useServiceStatus` + `buildSteps("create", …)`; сохранить
  `StatusBadge`, ссылки и рендер карточек; убрать дублирующую локальную логику поллинга
- [x] 5.2 Проверить `web/src/pages/CreateServicePage.tsx` — поток (POST → навигация на
  страницу прогресса) сохраняется; синхронная ошибка (409 имя занято) остаётся тостом
- [x] 5.3 Обновить `web/src/pages/*.test.tsx` (CreateServicePage, ServiceProgressPage)
  под новый UX (степпер вместо строки-сообщения)

## 6. Унификация карточек операций (тост → степпер)

- [x] 6.1 `web/src/components/OwnersCard.tsx` — убрать `toast.success`; после принятой
  мутации инвалидировать `["service", …]` (поднимает вырожденный степпер смены владельцев
  на странице); синхронные 403/409 остаются тостом (`lib/errors`)
- [x] 6.2 `web/src/components/DecommissionCard.tsx` — убрать `toast.success`; ход показывать
  степпером операции decommission (пометка точки невозврата); синхронные 403/409/422 —
  тостом
- [x] 6.3 `web/src/components/TransferCard.tsx` — убрать `toast.success`; ход показывать
  степпером операции transfer (пометка точки невозврата); синхронные 403/409/422 — тостом
- [x] 6.4 Связать карточки и страницу прогресса: после запуска операции страница
  (через хук) показывает соответствующий степпер до терминала (единый компонент)
- [x] 6.5 Обновить `web/src/components/{Owners,Decommission,Transfer}Card.test.tsx` —
  успех ведёт к степперу/инвалидации (а не success-тосту); синхронные ошибки — тост

## 7. Проверка и зелёный CI

- [x] 7.1 `web`: `npm run typecheck` и `npm test` (vitest) — зелёные; новые тесты проходят
- [x] 7.2 Убедиться, что контракт/кодоген НЕ менялись: `gen:check` зелёный без
  перегенерации; `openapi.yaml`, `web/src/api/*`, `web/public/openapi.yaml` не правлены
  руками
- [ ] 7.3 Полный CI (go test всех модулей -race -shuffle, golangci-lint, govulncheck,
  gen:check, openapi-lint, web-test, integration) зелёный; открыть PR с ветки
- [ ] 7.4 После merge — отдельным PR `/opsx:archive` (sync+archive, по образцу #59/#67/#69)
