# perimeter-rest Specification (delta)

## ADDED Requirements

### Requirement: Горизонтальные REST-ручки чтения каталога IAM

Периметр (ADR-0009) ДОЛЖЕН (MUST) предоставлять ГОРИЗОНТАЛЬНЫЕ (не project-scoped)
read-only ручки IAM-админки: `GET /iam/roles` (список ролей),
`GET /iam/permissions` (все права), `GET /iam/roles/{role}/permissions` (права
роли), `GET /iam/subjects` (субъекты с их ролями, keyset-пагинация
`page_size`/`page_token`), `GET /iam/subjects/{subject}/roles` (роли субъекта).
gateway ДОЛЖЕН (MUST) ПЕРЕД проксированием каждой ручки вызывать `CheckAccess`
с `resource="iam:global"`, `action="read"` (fail-closed) и НЕ ДОЛЖЕН (MUST NOT)
раскрывать внутренние ошибки клиенту. OpenAPI остаётся единственным источником
правды; TS-клиент/zod регенерируются (`gen:check`). Перечисление субъектов
отражает `DISTINCT subject` модели (субъекты без ролей не возвращаются).

#### Scenario: Успешное чтение каталога

- **GIVEN** субъект с правом `(read, iam:global)`
- **WHEN** выполняется `GET /iam/roles` (и `GET /iam/subjects`)
- **THEN** gateway после `CheckAccess` проксирует в IDM и возвращает `200` с
  ролями (и страницей субъектов с ролями + `next_page_token`), без раскрытия
  внутренних деталей

#### Scenario: Нет права read → 403 (fail-closed)

- **GIVEN** субъект без права `(read, iam:global)` ИЛИ IDM недоступен
- **WHEN** выполняется любой `GET /iam/*`
- **THEN** gateway отвечает `403`, запрос в IDM-чтение не проксируется, деталь —
  только в лог по ключу slog `err`

#### Scenario: Права несуществующей роли → 404

- **GIVEN** роли `no-such-role` нет
- **WHEN** выполняется `GET /iam/roles/no-such-role/permissions`
- **THEN** gateway отвечает `404` (из `NotFound`), без внутренних деталей

### Requirement: REST-ручки назначения и снятия роли субъекту

Периметр ДОЛЖЕН (MUST) предоставлять `POST /iam/subjects/{subject}/roles/{role}`
(назначить роль) и `DELETE /iam/subjects/{subject}/roles/{role}` (снять роль),
проксируемые в `RoleAdminService.AssignRole`/`RevokeRole`. gateway ДОЛЖЕН (MUST)
ПЕРЕД проксированием вызывать `CheckAccess` с `resource="iam:global"`,
`action="write"` (fail-closed → 403). Обе ручки ДОЛЖНЫ (MUST) быть идемпотентными:
повторное назначение уже имеющейся роли и снятие отсутствующей связки возвращают
`200`. Ответ ДОЛЖЕН (MUST) нести актуальный набор ролей субъекта
(`{subject, roles[]}`), а не пустое тело (для рантайм-валидации zod и обновления
UI). Несуществующая роль при назначении → `404` (из `NotFound`); пустые
`subject`/`role` → `400` (из `InvalidArgument`). Конкурентный конфликт состояния не
возникает (операции идемпотентны), поэтому `409`/`422` для этих ручек НЕ
применяются. Внутренние ошибки наружу НЕ раскрываются.

#### Scenario: Успешное назначение роли

- **GIVEN** субъект с правом `(write, iam:global)`, роль `iam-admin` существует
- **WHEN** выполняется `POST /iam/subjects/alice/roles/iam-admin`
- **THEN** роль назначается, возвращается `200` с актуальным набором ролей
  субъекта `alice`

#### Scenario: Идемпотентный повтор назначения и снятия

- **GIVEN** у субъекта `alice` уже есть роль `iam-admin`
- **WHEN** повторно `POST .../roles/iam-admin`, затем дважды
  `DELETE .../roles/iam-admin`
- **THEN** каждый вызов возвращает `200` с актуальным набором ролей (без ошибки)

#### Scenario: Нет права write → 403 (fail-closed)

- **GIVEN** субъект без права `(write, iam:global)` ИЛИ IDM недоступен
- **WHEN** выполняется `POST`/`DELETE /iam/subjects/{subject}/roles/{role}`
- **THEN** gateway отвечает `403`, мутация не проксируется, деталь — только в лог

#### Scenario: Несуществующая роль при назначении → 404

- **GIVEN** роли `no-such-role` нет
- **WHEN** выполняется `POST /iam/subjects/alice/roles/no-such-role`
- **THEN** gateway отвечает `404` (из `NotFound`), привязка не создаётся

#### Scenario: Пустой subject или role → 400

- **WHEN** выполняется мутация с пустым `subject` или `role`
- **THEN** gateway отвечает `400` (из `InvalidArgument`), без раскрытия деталей
