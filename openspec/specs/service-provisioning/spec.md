# service-provisioning Specification

## Purpose
TBD - created by archiving change create-service-workflow. Update Purpose after archive.
## Requirements
### Requirement: Temporal-workflow «Создание сервиса»

Провизия сервиса ДОЛЖНА (MUST) выполняться durable Temporal-workflow «Создание сервиса», определённым в `services/projects` и исполняемым DevInfra worker'ом на task-queue `devinfra` (ADR-0001). Workflow ДОЛЖЕН (MUST) последовательно выполнять activities провизии в порядке: GitLab (репозиторий) → Harbor (директория + Robot Account) → Vault (политики + AppRole) → инъекция секретов в CI/CD-переменные GitLab. Внешние вызовы ДОЛЖНЫ (MUST) идти только через activities с заданными `RetryPolicy`, таймаутами (`StartToCloseTimeout`) и heartbeat для долгих операций. Workflow-код ДОЛЖЕН (MUST) быть детерминированным (без обращений к времени/случайности/сети вне activities).

#### Scenario: Happy-path — все ресурсы созданы

- **GIVEN** запись каталога со `status=CREATING` и валидные `(project, name)`
- **WHEN** workflow «Создание сервиса» исполняется и все activities (GitLab, Harbor, Vault, инъекция секретов) завершаются успешно
- **THEN** activities выполняются в порядке GitLab → Harbor → Vault → инъекция секретов, и workflow завершается успешно без запуска компенсаций

#### Scenario: Ретраи транзиентного сбоя activity

- **GIVEN** activity провизии, временно возвращающая retryable-ошибку
- **WHEN** workflow исполняет эту activity
- **THEN** activity повторяется согласно `RetryPolicy` (с backoff) и при последующем успехе workflow продолжается без перехода в ветку компенсации

### Requirement: Идемпотентность запуска через детерминированный WorkflowID

Запуск workflow ДОЛЖЕН (MUST) использовать детерминированный `WorkflowID`, производный от `(project, name)` (или идентификатора записи каталога), с политикой переиспользования, исключающей параллельный дубль для одного сервиса. Повторный запуск создания одного и того же сервиса НЕ ДОЛЖЕН (MUST NOT) порождать второй конкурентный workflow или дублировать провизию. Activities ДОЛЖНЫ (MUST) быть идемпотентны: повторное выполнение шага при уже созданном ресурсе НЕ ДОЛЖНО (MUST NOT) приводить к ошибке или дубликату.

#### Scenario: Повторный запуск не создаёт второй workflow

- **GIVEN** уже запущенный workflow создания для `(project=p1, name=svc)`
- **WHEN** поступает повторный запрос на создание того же `(p1, svc)`
- **THEN** новый конкурентный workflow с тем же `WorkflowID` не стартует (переиспользование/дедупликация), провизия не дублируется

#### Scenario: Идемпотентное повторение activity

- **GIVEN** activity GitLab, репозиторий для которой уже создан предыдущей попыткой
- **WHEN** activity выполняется повторно при ретрае
- **THEN** она распознаёт уже существующий ресурс и завершается успешно без создания дубликата

### Requirement: Saga-компенсации с полным откатом при недоступности Vault

При фатальном (non-retryable `ApplicationError`) сбое провизии workflow ДОЛЖЕН (MUST) выполнить компенсации в обратном порядке относительно успешно выполненных шагов (ADR-0005). При окончательной недоступности Vault (ретраи исчерпаны) workflow ДОЛЖЕН (MUST) выполнить полный откат: удалить созданную Harbor-директорию и GitLab-репозиторий. Компенсации ДОЛЖНЫ (MUST) быть идемпотентными. Если сама компенсация окончательно не удалась, workflow ДОЛЖЕН (MUST) перевести запись в `FAILED` и выпустить alert оператору — молчаливое игнорирование сбоя компенсации ЗАПРЕЩЕНО (MUST NOT).

#### Scenario: Полный откат при окончательной недоступности Vault

- **GIVEN** успешно созданы GitLab-репозиторий и Harbor-директория, а activity Vault исчерпала ретраи (non-retryable)
- **WHEN** workflow переходит в ветку компенсации
- **THEN** выполняются компенсации в обратном порядке: удаление Harbor-директории, затем удаление GitLab-репозитория, и сервис остаётся «ничего не создано»

#### Scenario: Сбой самой компенсации → FAILED + alert

- **GIVEN** ветка компенсации, в которой удаление GitLab-репозитория окончательно падает
- **WHEN** workflow исчерпывает попытки компенсации
- **THEN** запись каталога переводится в `FAILED` и выпускается alert оператору (сбой не проглатывается молча)

#### Scenario: non-retryable ошибка ведёт в компенсацию, а не в ретрай

- **GIVEN** activity, вернувшая non-retryable `ApplicationError` (например, валидационный отказ внешней системы)
- **WHEN** workflow получает эту ошибку
- **THEN** activity не повторяется, и workflow сразу переходит в ветку компенсации

### Requirement: Регистрация workflow/activities и живость worker'а

DevInfra worker ДОЛЖЕН (MUST) регистрировать workflow «Создание сервиса» и все activities провизии на task-queue `devinfra` и реально поллить её. Эндпоинт `/readyz` worker'а ДОЛЖЕН (MUST) отражать реальный сигнал живости — готовность сообщается только когда worker запущен и поллит task-queue; при незапущенном/остановленном worker'е `/readyz` ДОЛЖЕН (MUST) возвращать неуспех.

#### Scenario: Worker готов, когда поллит очередь

- **GIVEN** запущенный DevInfra worker с зарегистрированными workflow/activities
- **WHEN** выполняется запрос к `/readyz`
- **THEN** проверка `worker` проходит и эндпоинт сообщает готовность

#### Scenario: Worker не готов до старта/после остановки

- **GIVEN** worker ещё не запущен (или остановлен)
- **WHEN** выполняется запрос к `/readyz`
- **THEN** эндпоинт возвращает неуспешный статус с указанием неготовой зависимости `worker`

### Requirement: Тестовое покрытие workflow temporal testsuite

Workflow ДОЛЖЕН (MUST) покрываться тестами на базе Temporal testsuite, проверяющими happy-path И ветки компенсаций/ретраев с замоканными activities. Тесты ДОЛЖНЫ (MUST) быть table-driven и использовать `t.Parallel()`; в пакетах с горутинами ДОЛЖЕН (MUST) применяться goleak. Стаб/in-memory активности ДОЛЖНЫ (MUST) проходить в дефолтном прогоне без внешних систем; реально-внешние тесты (если есть) ДОЛЖНЫ (MUST) быть под тегом сборки `integration`.

#### Scenario: Happy-path в testsuite

- **WHEN** выполняется тест workflow с замоканными успешными activities
- **THEN** workflow завершается успешно, activities вызываются в ожидаемом порядке, компенсации не вызываются

#### Scenario: Ветка компенсации в testsuite

- **GIVEN** мок activity Vault, возвращающий non-retryable ошибку
- **WHEN** выполняется тест workflow
- **THEN** в истории вызываются компенсации Harbor и GitLab в обратном порядке, и итог фиксируется как откат

#### Scenario: Дефолтный прогон не требует внешних систем

- **WHEN** выполняется `go test ./...` без тега `integration`
- **THEN** тесты workflow и activities проходят на стабах/in-memory без обращения к GitLab/Vault/Harbor

### Requirement: Авторизация запуска создания через IDM CheckAccess

gRPC-вход `CreateService` сервиса проектов ДОЛЖЕН (MUST) авторизовать вызов
через IDM `CheckAccess(subject, "project:"+project, "create")` ПЕРЕД любыми
доменными записями и запуском workflow (defense-in-depth: проверка выполняется
даже если шлюз обойдён). `subject` ДОЛЖЕН (MUST) извлекаться из
`auth.ClaimsFromContext`. При `allowed=false` ИЛИ недоступности/ошибке вызова
IDM сервис ДОЛЖЕН (MUST) вернуть `codes.PermissionDenied` (fail-closed) и НЕ
создавать запись каталога и НЕ запускать workflow; внутренние детали НЕ ДОЛЖНЫ
(MUST NOT) раскрываться клиенту. `AUTH_DISABLED` допустим ТОЛЬКО локально.

#### Scenario: Разрешено — создание продолжается

- **GIVEN** субъект с правом `(create, project:<p>)`
- **WHEN** вызывается gRPC `CreateService(<p>, name)`
- **THEN** `CheckAccess` возвращает `allowed=true`, и создание продолжается
  (запись каталога + запуск workflow)

#### Scenario: Отказ RBAC — PermissionDenied без побочных эффектов

- **GIVEN** субъект без права `(create, project:<p>)`
- **WHEN** вызывается gRPC `CreateService(<p>, name)`
- **THEN** возвращается `codes.PermissionDenied`, запись каталога не создаётся и
  workflow не запускается, внутренние детали не раскрываются

#### Scenario: IDM недоступен — fail-closed

- **GIVEN** сервис IDM недоступен или вернул ошибку вызова
- **WHEN** вызывается gRPC `CreateService`
- **THEN** возвращается `codes.PermissionDenied` (доступ не предоставляется
  молча), доменных записей не происходит

### Requirement: Activities синхронизации владельцев в DevInfra worker

DevInfra worker ДОЛЖЕН (MUST) реализовать и зарегистрировать activities
синхронизации владельцев по образцу существующих (RetryPolicy/таймауты/
heartbeat, классификация неустранимых ошибок в non-retryable
ApplicationError для ветки компенсации): синхронизация участников/ролей в GitLab
по diff add/remove, синхронизация политик доступа в Vault, guarded-CAS-запись
набора owners в каталог и синхронизация ролей в IDM с инвалидацией кэша решений.
Каждая прямая activity ДОЛЖНА (MUST) иметь идемпотентную компенсацию
(восстановление прежнего состава участников GitLab / прежних политик Vault).
Внешние вызовы к GitLab/Vault ДОЛЖНЫ (MUST) проходить через моки интеграций с
SSRF-guard на исходящих; секреты НЕ ДОЛЖНЫ (MUST NOT) логироваться в открытом
виде. Имена activities ДОЛЖНЫ (MUST) объявляться в публичном контракте workflow
(пакет `changeowners`) и вызываться по строковым именам.

#### Scenario: Синхронизация участников GitLab по diff

- **GIVEN** diff владельцев `add={bob}`, `remove={dave}`
- **WHEN** выполняется activity синхронизации участников GitLab
- **THEN** `bob` добавляется, `dave` удаляется в моке GitLab; повторный прогон с
  тем же diff идемпотентен (без ошибок и дублей)

#### Scenario: Компенсация восстанавливает прежний состав

- **GIVEN** участники GitLab синхронизированы (add=`{bob}`), а следующий шаг
  завершился non-retryable-ошибкой до точки невозврата
- **WHEN** выполняется компенсация GitLab
- **THEN** прежний состав участников восстанавливается идемпотентно (повторный
  прогон компенсации безопасен)

#### Scenario: Неустранимая ошибка уходит в ветку компенсации

- **GIVEN** мок Vault возвращает ошибку валидации/доступа/конфликта
- **WHEN** выполняется activity синхронизации политик Vault
- **THEN** ошибка оборачивается в non-retryable ApplicationError, workflow
  уходит в ветку компенсации (не бесконечные ретраи)

### Requirement: guarded-CAS-запись owners и IDM-синхронизация как activities

Запись набора владельцев в каталог ДОЛЖНА (MUST) выполняться activity через
guarded-CAS по версии (`expected_version`), а конфликт (`RowsAffected==0`) —
оборачиваться в non-retryable-ошибку (ретраем не исправить). Синхронизация ролей
IDM ДОЛЖНА (MUST) выдавать роль владельца добавленным субъектам и отзывать у
удалённых через управляющий gRPC IDM, после чего инвалидировать кэш решений по
затронутым субъектам; эти activities ДОЛЖНЫ (MUST) быть идемпотентными.

#### Scenario: guarded-CAS-запись owners при конфликте версии

- **GIVEN** в каталоге `owners_version` изменилась конкурентной операцией
- **WHEN** выполняется activity записи owners с устаревшим `expected_version`
- **THEN** возвращается non-retryable-конфликт (`errs.ErrConflict`), workflow
  трактует это как сбой до точки невозврата

#### Scenario: IDM-синхронизация ролей и инвалидация кэша

- **GIVEN** diff `add={bob}`, `remove={dave}` и роль владельца `owner:project:p1`
- **WHEN** выполняется activity IDM-синхронизации
- **THEN** `bob` получает роль, `dave` теряет роль, кэш решений по `bob` и `dave`
  инвалидируется; повторный прогон идемпотентен
