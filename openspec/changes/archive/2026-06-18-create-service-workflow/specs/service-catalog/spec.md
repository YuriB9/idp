## MODIFIED Requirements

### Requirement: Usecase и gRPC-чтение каталога

Сервис `projects` ДОЛЖЕН (MUST) реализовать `usecase`-слой поверх repository и доменную реализацию gRPC: `GetService` (чтение из каталога; отсутствие записи → `codes.NotFound`) и `ListServices` (keyset-пагинация). Метод `CreateService` ДОЛЖЕН (MUST) вставлять запись со `status=CREATING` в одном шаге с запуском Temporal-workflow «Создание сервиса»: запись фиксируется первой (status=CREATING), затем стартует workflow с детерминированным `WorkflowID`; запуск workflow выполняется после успешной вставки. Сервис НЕ ДОЛЖЕН (MUST NOT) отдавать клиенту `err.Error()` внутренних ошибок.

#### Scenario: GetService возвращает существующую запись

- **GIVEN** в каталоге есть запись `(project=p1, name=svc, status=ACTIVE)`
- **WHEN** вызывается `GetService(project=p1, name=svc)`
- **THEN** возвращается ответ со `status=SERVICE_STATUS_ACTIVE` и совпадающими `project`/`name`

#### Scenario: GetService для отсутствующей записи → NotFound

- **GIVEN** в каталоге нет записи `(project=p1, name=missing)`
- **WHEN** вызывается `GetService(project=p1, name=missing)`
- **THEN** возвращается gRPC-статус `codes.NotFound`, без раскрытия внутренних деталей ошибки

#### Scenario: CreateService вставляет запись и запускает workflow

- **WHEN** вызывается `CreateService(project=p1, name=svc)`
- **THEN** в каталоге появляется запись со `status=CREATING`, стартует Temporal-workflow с детерминированным `WorkflowID`, и клиенту возвращается идентификатор/статус `CREATING`

#### Scenario: Запись со status=CREATING фиксируется до старта workflow

- **GIVEN** вызов `CreateService(project=p1, name=svc)`
- **WHEN** обрабатывается запрос
- **THEN** сначала фиксируется запись каталога со `status=CREATING`, и только затем выполняется запуск workflow (workflow не стартует при неуспешной вставке)

## ADDED Requirements

### Requirement: Переходы жизненного цикла создания через guarded-CAS

По завершении workflow «Создание сервиса» статус записи ДОЛЖЕН (MUST) обновляться guarded-CAS-переходом через `repository.TransitionStatus` с ожидаемым исходным статусом `CREATING` (ADR-0004). При успешной провизии выполняется переход с `expected=CREATING`, `next=ACTIVE`; при фатальном сбое (после компенсаций или при сбое компенсации) — переход с `expected=CREATING`, `next=FAILED`. При `RowsAffected==0` (статус уже не `CREATING`) слой ДОЛЖЕН (MUST) возвращать `errs.ErrConflict` и НЕ ДОЛЖЕН (MUST NOT) молча перетирать статус.

#### Scenario: Успешная провизия → CREATING переходит в ACTIVE

- **GIVEN** запись со `status=CREATING` и успешно завершённый workflow провизии
- **WHEN** выполняется guarded-CAS-переход с `expected=CREATING`, `next=ACTIVE`
- **THEN** `RowsAffected==1`, статус становится `ACTIVE`, `updated_at` обновляется

#### Scenario: Фатальный сбой → CREATING переходит в FAILED

- **GIVEN** запись со `status=CREATING` и workflow, завершившийся фатально (компенсации выполнены или сама компенсация упала)
- **WHEN** выполняется guarded-CAS-переход с `expected=CREATING`, `next=FAILED`
- **THEN** `RowsAffected==1`, статус становится `FAILED`

#### Scenario: Конфликт при неожиданном исходном статусе

- **GIVEN** запись, уже не находящаяся в `CREATING` (например, `FAILED`)
- **WHEN** workflow пытается перейти с `expected=CREATING`
- **THEN** `RowsAffected==0`, возвращается `errs.ErrConflict`, статус не перетирается молча
