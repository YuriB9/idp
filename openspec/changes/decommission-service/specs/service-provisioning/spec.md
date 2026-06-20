# service-provisioning Specification (delta)

## ADDED Requirements

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
