# service-provisioning Specification (delta)

## ADDED Requirements

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
