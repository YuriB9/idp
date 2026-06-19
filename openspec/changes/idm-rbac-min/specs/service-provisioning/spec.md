## ADDED Requirements

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
