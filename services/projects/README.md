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
