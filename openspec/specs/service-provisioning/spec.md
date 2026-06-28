# service-provisioning Specification

## Purpose
TBD - created by archiving change create-service-workflow. Update Purpose after archive.
## Requirements
### Requirement: Temporal-workflow «Создание сервиса»

Провизия сервиса ДОЛЖНА (MUST) выполняться durable Temporal-workflow «Создание сервиса», определённым в `services/projects` и исполняемым DevInfra worker'ом на task-queue `devinfra` (ADR-0001). Workflow ДОЛЖЕН (MUST) последовательно выполнять activities провизии в порядке: GitLab (репозиторий) → GitLab (назначение владельцев участниками репозитория) → Harbor (директория + Robot Account) → Vault (политики + AppRole) → Vault (политики доступа владельцев) → инъекция секретов в CI/CD-переменные GitLab → перевод записи в `ACTIVE` → IDM (выдача ролей владельцев). Шаги назначения владельцев (GitLab members, Vault policies, IDM owner roles) ДОЛЖНЫ (MUST) переиспользовать activities синхронизации владельцев (`GitLabSyncMembers`/`VaultSyncOwners`/`IDMSyncOwnerRoles`, ADR-0011) с `add = owners`, `remove = []`, где `owners` берутся из записи каталога. Внешние вызовы ДОЛЖНЫ (MUST) идти только через activities с заданными `RetryPolicy`, таймаутами (`StartToCloseTimeout`) и heartbeat для долгих операций. Workflow-код ДОЛЖЕН (MUST) быть детерминированным (без обращений к времени/случайности/сети вне activities). Выдача ролей владельцев в IDM ДОЛЖНА (MUST) выполняться ПОСЛЕ перевода записи в `ACTIVE`; её сбой НЕ ДОЛЖЕН (MUST NOT) откатывать созданные ресурсы (каталог — источник правды), а ДОЛЖЕН (MUST) фиксироваться алертом оператору (ADR-0005/0008).

#### Scenario: Happy-path — ресурсы созданы и владельцы назначены

- **GIVEN** запись каталога со `status=CREATING`, валидные `(project, name)` и непустой набор `owners`
- **WHEN** workflow «Создание сервиса» исполняется и все activities завершаются успешно
- **THEN** activities выполняются в порядке GitLab repo → GitLab members → Harbor → Vault setup → Vault policies → инъекция секретов → активация → IDM roles, владельцы из каталога получают членство в GitLab, политики доступа Vault и роли владельца в IDM, и workflow завершается успешно без запуска компенсаций

#### Scenario: Ретраи транзиентного сбоя activity

- **GIVEN** activity провизии, временно возвращающая retryable-ошибку
- **WHEN** workflow исполняет эту activity
- **THEN** activity повторяется согласно `RetryPolicy` (с backoff) и при последующем успехе workflow продолжается без перехода в ветку компенсации

#### Scenario: Сбой выдачи ролей IDM после активации не откатывает создание

- **GIVEN** ресурсы созданы, владельцы назначены в GitLab/Vault, запись переведена в `ACTIVE`
- **WHEN** activity выдачи ролей владельцев в IDM окончательно не удаётся (исчерпаны ретраи)
- **THEN** созданные ресурсы НЕ откатываются, статус остаётся `ACTIVE`, и фиксируется алерт оператору (структурный лог `error`) без молчаливого проглатывания

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

### Requirement: Activities обратных операций вывода из эксплуатации

DevInfra worker ДОЛЖЕН (MUST) реализовать activities обратных операций (отзыв
доступа, а не удаление ресурса) для workflow «Вывод из эксплуатации», по образцу
существующих activities провижна, с SSRF-guard на всех исходящих, таймаутами,
heartbeat и RetryPolicy: (1) GitLab — archive репозитория + отзыв доступов
участников; (2) Harbor — перевод проекта в read-only + отзыв Robot-аккаунта; (3)
Vault — отзыв активных SecretID/токенов сервиса (немедленное прекращение доступа).
Decommission НЕ ДОЛЖЕН (MUST NOT) удалять ресурсы (это не `Delete`/`Teardown`
компенсаций провижна). Шаги ДОЛЖНЫ (MUST) быть идемпотентными. Для шагов до точки
невозврата (GitLab/Harbor) ДОЛЖНЫ (MUST) существовать идемпотентные компенсации
(GitLab → unarchive, Harbor → writable). Секреты/токены НЕ ДОЛЖНЫ (MUST NOT)
логироваться. В дефолтном прогоне activities работают против моков интеграций.

#### Scenario: GitLab archive с отзывом доступов

- **GIVEN** активный сервис с репозиторием GitLab (мок)
- **WHEN** выполняется activity archive + revoke
- **THEN** репозиторий помечается archived, доступы участников отозваны; повторный
  вызов идемпотентен; компенсация `unarchive` восстанавливает состояние

#### Scenario: Harbor read-only с отзывом Robot

- **GIVEN** проект Harbor сервиса (мок)
- **WHEN** выполняется activity перевода в read-only + отзыва Robot
- **THEN** проект становится read-only, Robot отозван; повторный вызов идемпотентен;
  компенсация `writable` восстанавливает состояние

#### Scenario: Vault отзыв активных SecretID/токенов (необратимо)

- **GIVEN** AppRole/секреты сервиса в Vault (мок)
- **WHEN** выполняется activity отзыва активных SecretID/токенов
- **THEN** активные SecretID/токены отозваны (доступ немедленно прекращён);
  повторный вызов идемпотентен; компенсации нет (необратимый отзыв — точка
  невозврата)

#### Scenario: SSRF-guard на исходящих

- **GIVEN** настроенные адреса GitLab/Harbor/Vault
- **WHEN** activity делает исходящий запрос
- **THEN** применяется SSRF-guard (валидация URL + guarded dial), запрос к
  внутренним/запрещённым адресам отклоняется

### Requirement: Activity перевода каталога в decommissioned

DevInfra worker ДОЛЖЕН (MUST) предоставлять activity перевода статуса каталога
`ACTIVE→DECOMMISSIONED` (обёртка над guarded-CAS repository, по образцу
`CatalogTransitionActive`), проставляющую `decommissioned_at` и возвращающую
`errs.ErrConflict` при `RowsAffected==0` (конкурентный конфликт). Activity ДОЛЖНА
(MUST) быть идемпотентной (повтор на уже выведенном сервисе — успех).

#### Scenario: Перевод каталога после отзыва доступов

- **GIVEN** доступы во внешних системах уже отозваны
- **WHEN** выполняется activity перевода каталога
- **THEN** статус становится `decommissioned`, `decommissioned_at` проставлен;
  повторный вызов идемпотентен

#### Scenario: Конфликт guarded-CAS

- **GIVEN** статус сервиса сменился конкурентной операцией
- **WHEN** выполняется activity перевода каталога
- **THEN** возвращается `errs.ErrConflict` (для обработки точки невозврата/алерта
  в workflow)

### Requirement: Предварительная проверка снятой нагрузки (LoadChecker)

DevInfra worker ДОЛЖЕН (MUST) предоставлять activity предварительной проверки
снятой нагрузки K8s через интерфейс `LoadChecker` (граница под будущий K8s-worker).
В MVP реализация ДОЛЖНА (MUST) опираться на явное предусловие (`load_drained`),
переданное вызывающей стороной, и НЕ ДОЛЖНА (MUST NOT) имитировать запрос к
несуществующему кластеру. Невыполненное предусловие ДОЛЖНО (MUST) возвращать
non-retryable ошибку предусловия (для отказа workflow до любых побочных эффектов).

#### Scenario: Предусловие выполнено

- **GIVEN** `load_drained=true`
- **WHEN** выполняется activity `EnsureLoadDrained`
- **THEN** проверка проходит без ошибки

#### Scenario: Предусловие не выполнено

- **GIVEN** `load_drained=false`
- **WHEN** выполняется activity `EnsureLoadDrained`
- **THEN** возвращается non-retryable ошибка предусловия, побочных эффектов нет

### Requirement: Activities переноса инфраструктуры (GitLab/Vault/Harbor)

DevInfra worker ДОЛЖЕН (MUST) реализовать activities переноса для workflow
«Перенос», по образцу существующих activities, с SSRF-guard на всех исходящих,
таймаутами, heartbeat и RetryPolicy: (1) GitLab — transfer репозитория в группу
target-проекта; (2) Vault — миграция путей: копия секретов `source→target` +
запись новых политик + очистка старых путей/политик; (3) Harbor — обновление
метаданных/прав директории образов под target-проект. Шаги Vault/Harbor/каталог
ПОСЛЕ transfer GitLab ДОЛЖНЫ (MUST) быть идемпотентными (форвард-only, повтор
безопасен). Transfer GitLab — ТОЧКА НЕВОЗВРАТА: в MVP чистая компенсация
(transfer-back) НЕ моделируется, поэтому до него компенсируется только каталог
(`CatalogAbortTransfer`), а после — форвард-only. Секреты/токены НЕ ДОЛЖНЫ (MUST
NOT) логироваться. В дефолтном прогоне activities работают против моков интеграций.

#### Scenario: GitLab transfer репозитория в новую группу

- **GIVEN** активный сервис с репозиторием GitLab (мок) в группе source
- **WHEN** выполняется activity transfer репозитория в группу target
- **THEN** репозиторий привязывается к группе target; повторный вызов идемпотентен
  (репозиторий уже в target → no-op)

#### Scenario: Vault миграция путей

- **GIVEN** секреты/политики сервиса в Vault (мок) по путям source
- **WHEN** выполняется activity миграции путей
- **THEN** секреты скопированы в пути target, записаны новые политики, старые пути/
  политики очищены; повторный вызов идемпотентен; секреты не логируются

#### Scenario: Harbor обновление метаданных/прав

- **GIVEN** директория образов сервиса в Harbor (мок)
- **WHEN** выполняется activity обновления метаданных/прав под target
- **THEN** метаданные/права директории отражают target-проект; повторный вызов
  идемпотентен

#### Scenario: SSRF-guard на исходящих

- **GIVEN** настроенные адреса GitLab/Harbor/Vault
- **WHEN** activity делает исходящий запрос
- **THEN** применяется SSRF-guard (валидация URL + guarded dial), запрос к
  внутренним/запрещённым адресам отклоняется

### Requirement: Activities смены проекта в каталоге (две фазы)

DevInfra worker ДОЛЖЕН (MUST) предоставлять activities смены проекта-владельца в
каталоге (обёртки над guarded-CAS repository, по образцу `CatalogDecommission`):
`CatalogBeginTransfer` (guarded-CAS `active→transferring` с проверкой свободы
`(target, name)`), `CatalogCommitTransfer` (guarded-CAS `transferring→active` +
`project=target`) и компенсацию `CatalogAbortTransfer` (guarded-CAS
`transferring→active`). Activities ДОЛЖНЫ (MUST) возвращать `errs.ErrConflict` при
`RowsAffected==0`/занятом имени (для обработки конфликта/алерта в workflow) и быть
идемпотентными (`CatalogCommitTransfer` повторно на уже перенесённой записи —
успех).

#### Scenario: Начало переноса (active→transferring)

- **GIVEN** сервис `(source, svc)` со `status=active`, пара `(target, svc)` свободна
- **WHEN** выполняется `CatalogBeginTransfer`
- **THEN** статус становится `transferring`; занятое `(target, svc)` →
  `errs.ErrConflict`; недопустимый статус → `errs.ErrPrecondition`

#### Scenario: Фиксация переноса (transferring→active, project=target)

- **GIVEN** сервис `(source, svc)` со `status=transferring`, пара `(target, svc)`
  свободна
- **WHEN** выполняется `CatalogCommitTransfer`
- **THEN** `project` становится `target`, статус — `active`; повторный вызов
  идемпотентен; конкурентная смена статуса → `errs.ErrConflict`

#### Scenario: Компенсация начала (transferring→active)

- **GIVEN** сервис `(source, svc)` со `status=transferring` (до точки невозврата)
- **WHEN** выполняется `CatalogAbortTransfer`
- **THEN** статус возвращается в `active`, `project` остаётся `source`

### Requirement: Перенос ролей владельцев между проектами

DevInfra worker ДОЛЖЕН (MUST) предоставлять activity переноса ролей владельцев в
IDM для каждого затронутого субъекта-владельца: `RevokeRole(subject,
owner:project:<source>)` + `AssignRole(subject, owner:project:<target>)` +
`InvalidateSubject(subject)` по ВСЕМ затронутым субъектам. Примитивы IDM ДОЛЖНЫ
(MUST) использоваться идемпотентно (повтор не ломает состояние); устаревшие `allow`
НЕ ДОЛЖНЫ (MUST NOT) оставаться в кэше после переноса. Activity — форвард-only шаг
после точки невозврата.

#### Scenario: Перенос ролей владельцев

- **GIVEN** владельцы сервиса имеют роль `owner:project:source`
- **WHEN** выполняется activity переноса ролей в target
- **THEN** для каждого владельца роль `owner:project:source` отозвана,
  `owner:project:target` выдана, кэш решений инвалидирован по затронутым субъектам;
  повторный вызов идемпотентен

### Requirement: Создание сервиса требует владельцев и фиксирует их атомарно

Usecase/репозиторий каталога ДОЛЖЕН (MUST) принимать набор владельцев как обязательную часть создания сервиса. Владельцы ДОЛЖНЫ (MUST) нормализоваться (отбрасывание пустых строк, удаление дублей, детерминированный порядок — тем же `normalizeOwners`, что и при смене владельцев) и записываться в каталог ВМЕСТЕ с записью сервиса в одной транзакции: строка `services` (status=`CREATING`) + строки `service_owners` + `owners_version = 1`. После нормализации пустой набор владельцев ДОЛЖЕН (MUST) приводить к ошибке `InvalidArgument` (создание не запускается, workflow не стартует). Сохраняется строгий порядок «запись фиксируется ПЕРВОЙ»: workflow «Создание сервиса» стартует только при успешной вставке; при сбое запуска запись best-effort переводится guarded-CAS `CREATING→FAILED`. Создатель НЕ ДОЛЖЕН (MUST NOT) добавляться во владельцы неявно — набор владельцев берётся только из запроса.

#### Scenario: Запись каталога вставляется вместе с владельцами

- **GIVEN** запрос создания с `name` и непустым набором `owners = {alice, bob}`
- **WHEN** usecase создаёт запись
- **THEN** в одной транзакции вставляются строка `services` (status `creating`), строки `service_owners` для `{alice, bob}` и `owners_version = 1`, после чего стартует workflow «Создание сервиса»; в каталоге НЕ существует промежуточного состояния записи без владельцев

#### Scenario: Создание без владельцев отклоняется до workflow

- **GIVEN** запрос создания, где `owners` пуст или становится пустым после нормализации
- **WHEN** usecase обрабатывает запрос
- **THEN** возвращается `InvalidArgument`, запись каталога НЕ вставляется и workflow НЕ стартует

#### Scenario: Сбой запуска workflow переводит запись в FAILED

- **GIVEN** запись с владельцами успешно вставлена (status `creating`, `owners_version=1`)
- **WHEN** запуск workflow «Создание сервиса» завершается ошибкой
- **THEN** запись best-effort переводится guarded-CAS `creating→failed`, владельцы в каталоге сохраняются

