# projects — каталог сервисов и доменные операции

gRPC `ProjectsService` поверх PostgreSQL-каталога: чтение (`GetService`/
`ListServices`), создание (`CreateService` → Temporal-workflow провизии) и смена
владельцев (`SetServiceOwners` → Temporal-workflow «Изменение владельцев»).
Переходы/изменения — guarded-CAS (ADR-0004); workflow с компенсациями
(ADR-0005/0008); RBAC fail-closed (ADR-0003/0010). См. docs/IDP_MVP_plan.md.

## Модель владельцев

Миграция `migrations/0002_add_service_owners.sql`:

- `service_owners(service_id, owner)` — нормализованный набор владельцев сервиса
  (FK на `services` с каскадным удалением; уникальность пары на уровне БД).
- `services.owners_version bigint` — версия набора владельцев для
  optimistic-concurrency.

Владелец (`owner`) — строковый идентификатор субъекта, совместимый с `sub` из JWT.
`Get`/`List` возвращают `owners` (детерминированный, лексикографический порядок)
и `owners_version`.

## Как сменить владельцев

Контракт **декларативный**: клиент передаёт полный желаемый набор владельцев и
текущую версию; сервер вычисляет diff (add/remove). Идемпотентно — повторная
отправка того же набора ничего не меняет.

Через периметр (REST, ADR-0009):

```
PUT /api/projects/{project}/services/{name}/owners
{ "owners": ["alice", "bob"], "owners_version": 4 }
```

- `owners_version` должна совпадать с актуальной (из `GET`), иначе **409**
  (конфликт — данные устарели, перечитайте и повторите).
- Несуществующий сервис → **404**; пустой/дублирующийся владелец → **400**.
- Нет права `change_owners` или недоступен IDM → **403** (fail-closed).

### Что происходит в workflow «Изменение владельцев» (Saga)

1. Синхронизация участников GitLab по diff (мок).
2. Синхронизация политик доступа Vault по diff (мок).
3. **Запись владельцев в каталог (guarded-CAS) — точка невозврата.**
4. Синхронизация ролей IDM: добавленным выдаётся роль `owner:project:<project>`,
   удалённым — отзывается; IDM инвалидирует кэш решений по затронутым субъектам.

Сбой **до** точки невозврата → идемпотентные компенсации (восстановление
прежнего состава GitLab/Vault), каталог не меняется. Сбой **после** точки
невозврата (IDM) → без молчаливого отката: идемпотентный ретрай, при исчерпании —
алерт оператору; каталог остаётся источником правды (ADR-0005/0008).

## Влияние на роли IDM и доступ

Состав владельцев определяет, кому выдана/отозвана роль `owner:project:<project>`
в IDM (права `read`/`list`/`change_owners` над ресурсом `project:<project>`).
После смены владельцев кэш решений IDM по затронутым субъектам инвалидируется,
поэтому новые/снятые доступы вступают в силу без «залипания» старых решений.

## Как проверить отказ/разрешение (локалка)

Стенд: `AUTH_DISABLED=true`, `AUTH_DISABLED_SUBJECT=demo-user`; IDM засеян так,
что `demo-user` имеет `(change_owners, project:demo)`.

- `PUT /api/projects/demo/services/<svc>/owners {"owners":["alice"],"owners_version":0}`
  → **200**, запускается workflow смены владельцев.
- Тот же запрос в проекте без права `change_owners` → **403**.
- Повтор с устаревшей `owners_version` → **409**.

## Перенос сервиса в другой проект (transfer)

Перенос меняет проект-владельца сервиса: **id записи каталога сохраняется**,
меняется колонка `project` (`source→target`), владельцы переезжают вместе с
записью. Это самый рискованный сценарий (ADR-0013): `transfer` репозитория GitLab
и миграция путей Vault частично **необратимы**.

`POST /api/projects/{project}/services/{name}/transfer` с телом
`{"target_project":"<target>"}`. Допустим только исходный статус `active`; на время
переноса сервис переходит в транзитный статус `transferring` (защита от
конкурентных операций + наблюдаемость).

### Что происходит в workflow «Перенос» (Saga)

1. каталог `active→transferring` (guarded-CAS, **компенсируемо**);
2. **`GitLabTransferRepo`** — перенос репозитория в группу target. **ТОЧКА
   НЕВОЗВРАТА**: чистая компенсация (transfer-back) в MVP не моделируется;
3. `VaultMigratePaths` — копия секретов `source→target` + новые политики + очистка
   старых;
4. `HarborUpdateMetadata` — обновление метаданных/прав директории под target;
5. каталог `transferring→active` + `project=target` (guarded-CAS);
6. `TransferOwnerRoles` — перенос ролей владельцев в IDM (`revoke
   owner:project:<source>` + `assign owner:project:<target>`) + инвалидация кэша.

Сбой **до** точки невозврата → идемпотентная компенсация каталога
(`transferring→active`), внешние системы не затронуты. Сбой **после** → НЕ
молчаливый откат: форвард-only ретраи идемпотентных шагов, при исчерпании — **алерт
оператору** (структурный лог по ключу `err`); сервис может остаться в
`transferring` до ручного довыполнения. Каталог — целевой источник правды.

### Авторизация (двусторонняя, fail-closed)

Перенос затрагивает ДВА проекта, поэтому требует ДВУХ прав: `transfer` на
`project:<source>` (вынести) И `transfer_in` на `project:<target>` (принять). Оба
проверяются `CheckAccess` на периметре и в `projects` (defense-in-depth). Без права
`transfer_in` на target нельзя «вынести» сервис в чужой проект.

### Как проверить отказ/предусловие/разрешение (локалка)

Стенд засеян так, что `demo-user` имеет `(transfer, project:demo)` и
`(transfer_in, project:demo2)`; проект `demo2` с ролью `owner:project:demo2`.

- `POST /api/projects/demo/services/<svc>/transfer {"target_project":"demo2"}`
  → **200**, запускается перенос (сервис → `transferring`, затем `active` в `demo2`).
- Перенос в проект без права `transfer_in` (например, `demo3`) → **403**.
- Имя `<svc>` уже занято в `demo2` → **409**.
- Сервис не в статусе `active` (например, `creating`) → **422**.

## Миграции

```bash
make migrate-projects                      # применить (goose up)
make migrate-projects GOOSE_CMD=down       # откатить последнюю
```

## Тесты

```bash
go test ./...                              # дефолт: стаб/in-memory + Temporal testsuite
PROJECTS_TEST_DSN="postgres://projects:projects@localhost:5432/projects?sslmode=disable" \
  go test -tags=integration ./internal/repository/...
```
