# service-contracts Specification (delta)

## ADDED Requirements

### Requirement: Контракт владельцев и SetServiceOwners в projects.v1

Контракт `proto/projects/v1` ДОЛЖЕН (MUST) расширяться полем `repeated string
owners` в сообщении `Service` и в ответах чтения (`GetServiceResponse`,
элементы `ListServicesResponse`), а также целочисленным полем версии владельцев
(`owners_version`) для optimistic-concurrency. ДОЛЖЕН (MUST) добавляться новый
RPC `SetServiceOwners(SetServiceOwnersRequest) returns (SetServiceOwnersResponse)`
с декларативной семантикой: запрос несёт `project`, `name`, полный желаемый набор
`owners` и `expected_version`; ответ — итоговый набор владельцев и новую версию.
Изменения ДОЛЖНЫ (MUST) быть аддитивными по номерам полей (не переиспользовать и
не менять смысл существующих тегов); добавление нового RPC в существующий сервис
помечается как BREAKING в комментарии контракта. Кодоген (`buf generate` →
`*.pb.go`) и TS-клиент портала ДОЛЖНЫ (MUST) регенерироваться, `gen:check`
зелёный. Все комментарии в `.proto` — на русском языке.

#### Scenario: Регенерация Go и TS из контракта

- **GIVEN** обновлённый `proto/projects/v1` с `owners`/`owners_version` и
  `SetServiceOwners`
- **WHEN** выполняется `buf generate` и регенерация TS-клиента
- **THEN** появляются типы/методы в `pkg/api/projects/v1` и в TS-клиенте `web`,
  а `gen:check` (proto+OpenAPI+TS) проходит без расхождений

#### Scenario: Аддитивность номеров полей

- **GIVEN** существующие поля `Service`/ответов с занятыми тегами
- **WHEN** добавляются `owners`/`owners_version`
- **THEN** новым полям присваиваются новые номера, существующие теги и их смысл
  не изменяются (обратная совместимость по wire для уже сериализованных данных)

### Requirement: Управляющий контракт ролей в idm.v1

Контракт `proto/idm/v1` ДОЛЖЕН (MUST) расширяться управляющими RPC выдачи и
отзыва роли субъекту (например, `AssignRole`/`RevokeRole` с полями `subject`,
`role`), пригодными для программной синхронизации ролей из доменного потока
смены владельцев. RPC ДОЛЖНЫ (MUST) быть идемпотентными (повторная выдача уже
имеющейся роли и повторный отзыв отсутствующей — успешны, без ошибки). Изменения
аддитивны; кодоген (`buf generate`) ДОЛЖЕН (MUST) проходить, `gen:check` зелёный.
Комментарии — на русском языке.

#### Scenario: Регенерация контракта IDM

- **GIVEN** обновлённый `proto/idm/v1` с `AssignRole`/`RevokeRole`
- **WHEN** выполняется `buf generate`
- **THEN** появляются методы в `pkg/api/idm/v1`, `gen:check` проходит

#### Scenario: Идемпотентность управляющих RPC

- **GIVEN** субъект уже имеет роль `R`
- **WHEN** повторно вызывается `AssignRole(subject, R)`
- **THEN** вызов завершается успешно без дублирования привязки; аналогично
  повторный `RevokeRole` отсутствующей роли — успешен
