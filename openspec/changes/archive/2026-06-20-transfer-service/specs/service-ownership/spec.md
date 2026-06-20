# service-ownership Specification (delta)

## ADDED Requirements

### Requirement: Программный перенос ролей владельцев между проектами

IDM (`RoleAdminService`) ДОЛЖЕН (MUST) обеспечивать программный перенос ролей
владельцев из доменного потока «Перенос сервиса»: для каждого затронутого субъекта
выполняются `RevokeRole(subject, owner:project:<source>)` и `AssignRole(subject,
owner:project:<target>)`, после чего вызывается `InvalidateSubject(subject)` по
ВСЕМ затронутым субъектам. Операции `AssignRole`/`RevokeRole`/`InvalidateSubject`
ДОЛЖНЫ (MUST) быть идемпотентными (повторный вызов не ломает состояние и не
ошибается). После переноса в кэше решений (DragonflyDB) НЕ ДОЛЖНО (MUST NOT)
оставаться устаревших `allow` для роли в source-проекте. В отличие от вывода из
эксплуатации (где per-project роль не отзывалась во избежание over-revoke,
ADR-0012), при переносе сервис целиком покидает source-проект, поэтому отзыв роли
в source и выдача в target корректны (ADR-0013).

#### Scenario: Перенос роли владельца demo→demo2

- **GIVEN** субъект-владелец имеет роль `owner:project:demo`, а роль
  `owner:project:demo2` сидирована
- **WHEN** доменный поток переноса вызывает перенос ролей
- **THEN** субъекту отзывается `owner:project:demo`, выдаётся `owner:project:demo2`,
  кэш по субъекту инвалидируется; последующий `CheckAccess` отражает актуальные
  роли без устаревшего `allow` на source

#### Scenario: Идемпотентный повтор переноса ролей

- **GIVEN** перенос ролей для субъекта уже выполнен
- **WHEN** перенос ролей повторяется (ретрай workflow)
- **THEN** `RevokeRole`/`AssignRole`/`InvalidateSubject` отрабатывают без ошибки и
  не меняют итоговое состояние ролей
