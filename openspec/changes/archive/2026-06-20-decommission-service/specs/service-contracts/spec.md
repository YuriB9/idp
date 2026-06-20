# service-contracts Specification (delta)

## ADDED Requirements

### Requirement: Контракт DecommissionService в projects.v1

Контракт `proto/projects/v1` ДОЛЖЕН (MUST) расширяться новым RPC
`DecommissionService(DecommissionServiceRequest) returns
(DecommissionServiceResponse)` для вывода сервиса из эксплуатации: запрос несёт
`project`, `name` и явное предусловие снятой нагрузки `load_drained`; ответ — итоговое
состояние сервиса (`status=DECOMMISSIONED`, `decommissioned_at`). В сообщение
`Service` и в ответы чтения (`GetServiceResponse`, элементы `ListServicesResponse`)
ДОЛЖНО (MUST) аддитивно добавляться поле времени вывода из эксплуатации
(`decommissioned_at`). Изменения ДОЛЖНЫ (MUST) быть аддитивными по номерам полей
(не переиспользовать и не менять смысл существующих тегов); добавление нового RPC в
существующий сервис помечается как BREAKING в комментарии контракта. Кодоген
(`buf generate` → `*.pb.go`) и TS-клиент портала ДОЛЖНЫ (MUST) регенерироваться,
`gen:check` зелёный. Все комментарии в `.proto` — на русском языке.

#### Scenario: Регенерация Go и TS из контракта

- **GIVEN** обновлённый `proto/projects/v1` с RPC `DecommissionService` и полем
  `decommissioned_at`
- **WHEN** выполняется `buf generate` и регенерация TS-клиента
- **THEN** появляются типы/метод в `pkg/api/projects/v1` и в TS-клиенте `web`, а
  `gen:check` (proto+OpenAPI+TS) проходит без расхождений

#### Scenario: Аддитивность номеров полей

- **GIVEN** существующие поля `Service`/ответов с занятыми тегами (включая `owners`,
  `owners_version`)
- **WHEN** добавляется `decommissioned_at`
- **THEN** новому полю присваивается новый номер, существующие теги и их смысл не
  изменяются (обратная совместимость по wire для уже сериализованных данных)

#### Scenario: Идемпотентная семантика контракта

- **GIVEN** контракт `DecommissionService`
- **WHEN** описывается семантика повторного вызова на уже выведенном сервисе
- **THEN** контракт (комментарий `.proto`) фиксирует идемпотентность: повторный
  вызов возвращает итоговое состояние без ошибки
