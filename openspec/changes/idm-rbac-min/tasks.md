## 1. Подготовка и impact-анализ

- [x] 1.1 Сверить scope с docs/IDP_MVP_plan.md (Этап 1 IDM, БЛОК 2, БЛОК 5),
  ADR-0003 и новым ADR-0010; зафиксировать расхождения, если есть
- [x] 1.2 Провести impact-анализ (`/understand-diff` на ветке change либо обход
  рёбер графа знаний от узлов `services/idm`, `services/gateway/handlers.go`,
  `services/projects/internal/grpcapi/server.go`): перечислить зависимые
  компоненты/тесты, подтвердить, что `proto/idm/v1` не меняется и кодоген не нужен
- [x] 1.3 Создать git-ветку `change/idm-rbac-min` от master (прямые коммиты в
  master запрещены)

## 2. IDM: схема и миграции (Postgres + goose)

- [x] 2.1 Создать каталог `services/idm/migrations` и миграцию
  `0001_create_rbac.sql` (обратимая Up/Down): таблицы `roles`, `permissions`,
  `role_permissions`, `subject_roles` с уникальными ограничениями (комментарии на русском)
- [x] 2.2 Добавить idempotent seed-миграцию `0002_seed_demo.sql`
  (`ON CONFLICT DO NOTHING`): роль `project-creator`, право `(create,
  project:demo)`, привязка демо-субъекта; явно помечена как локальные демо-данные
- [x] 2.3 Проверить применение/откат локально (`goose up`/`goose down`,
  `GOWORK=off`) и сверить демо-субъект с claims `AUTH_DISABLED` из `pkg/auth`

## 3. IDM: доменные слои и кэш

- [x] 3.1 Добавить в `services/idm/go.mod` зависимости `redis/go-redis/v9` и
  `golang.org/x/sync/singleflight`; подключить `pkg/db`
- [x] 3.2 Реализовать `internal/repository` (pgx): запрос решения по модели
  (EXISTS-цепочка subject→role→permission) и операции управления ролями в
  транзакции; интерфейс для подмены в тестах
- [x] 3.3 Реализовать `internal/cache` поверх DragonflyDB: ключ
  `idm:decision:<gen>:{subject}:{resource}:{action}`, TTL (вкл. negative
  caching), version-bump инвалидация (`idm:cache:gen`) + точечное удаление по субъекту
- [x] 3.4 Реализовать `internal/usecase` `CheckAccess`: cache GET → при промахе
  singleflight(БД) → cache SET; deny-by-default; fail-closed (ошибка БД → deny,
  ошибка кэша → деградация к БД, не разрешение)
- [x] 3.5 Заменить скелет `accessServer.CheckAccess` в `services/idm/main.go`
  реальной реализацией: валидация запроса (пустые поля → InvalidArgument),
  маппинг в `{allowed, reason}`, без раскрытия внутренних ошибок
- [x] 3.6 Подключить пул Postgres (`pkg/db`) и клиент DragonflyDB в `run()`;
  добавить content-aware `/readyz` с пингом Postgres И DragonflyDB

## 4. IDM: тесты

- [x] 4.1 Table-driven + `t.Parallel()` для логики `CheckAccess` (allow/deny/
  deny-by-default) на стаб/in-memory репозитории
- [x] 4.2 Тесты кэша: hit/miss, инвалидация, singleflight (N конкурентных
  промахов → один вызов БД через счётчик в стабе); `goleak` в пакетах с горутинами
- [x] 4.3 Тест fail-closed: недоступная БД → deny; недоступный кэш → чтение БД
- [x] 4.4 Integration-тест репозитория с pgx и тест-БД (применение миграций через embed/goose)

## 5. Gateway: вызов RBAC

- [x] 5.1 В `services/gateway/main.go` задействовать клиент `idmv1` (убрать
  `_ =`) и передать его в `servicesAPI`
- [x] 5.2 В `services/gateway/handlers.go` вызывать IDM `CheckAccess` в
  `create`/`list`/`get` перед проксированием: subject из
  `auth.ClaimsFromContext`, resource `project:<project>`, action `create`/`list`/`read`
- [x] 5.3 Маппинг отказа: `allowed=false` или ошибка вызова IDM → HTTP 403
  (fail-closed); добавить `codes.PermissionDenied → 403` в `httpFromGRPC`; не
  раскрывать детали
- [x] 5.4 Тесты gateway со стаб-клиентом IDM (без сети): deny → 403, IDM
  недоступен → 403, тело без внутренних деталей

## 6. Projects: вызов RBAC (defense-in-depth)

- [x] 6.1 Добавить `idmv1` в `services/projects/go.mod`; внедрить клиент IDM в
  `grpcapi.Server` (конструктор + wiring в `main.go`)
- [x] 6.2 Заменить заглушку `authorize()` вызовом IDM `CheckAccess(subject,
  "project:"+project, "create")`; deny/ошибка → `codes.PermissionDenied`, без
  побочных эффектов
- [x] 6.3 Тесты projects со стаб-клиентом IDM: deny → PermissionDenied, IDM
  недоступен → PermissionDenied, запись каталога/workflow не создаются

## 7. Локалка и документация

- [x] 7.1 Создать `services/idm/migrate.Dockerfile` по образцу
  `services/projects/migrate.Dockerfile` (goose из `./tools`, контекст — корень)
- [x] 7.2 Добавить сервис `migrate-idm` в `deploy/compose/docker-compose.yml`;
  `idm` зависит от него через `service_completed_successfully`
- [x] 7.3 Проверить сквозной сценарий портала «Создание сервиса» при включённом
  RBAC (демо-роль засеяна → allow; субъект без роли → 403)
- [x] 7.4 README/инструкция: устройство ролей, как выдать право (SQL/seed), как
  проверить отказ и разрешение

## 8. Проверка и PR

- [x] 8.1 Прогнать локально тесты всех затронутых модулей, golangci-lint
  (включая errname/paralleltest), govulncheck, `gen:check`
- [x] 8.2 Открыть PR из `change/idm-rbac-min`, добиться зелёного CI (вкл.
  integration); архивирование change — только после merge в master
