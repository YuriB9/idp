# ADR (изменение: create-service-portal)

Этот файл — индекс архитектурных решений данного изменения. Канонические ADR
лежат в `docs/adr/NNNN-kebab-title.md` (вне `openspec/`).

## Новые ADR

- **ADR-0009 — Форма REST-ресурсов периметра** (`docs/adr/0009-perimeter-rest-resource-shape.md`):
  доменные операции периметра адресуются проектно-скоупленными путями
  `/projects/{project}/services[/{name}]`; каркасный `GET /services` удаляется
  (**BREAKING**); формы согласованы с gRPC `projectsv1`.

## Реализуемые существующие ADR

- **ADR-0002 — gRPC внутри / OpenAPI на периметре**: реализуются доменные
  REST-ручки gateway поверх gRPC `projectsv1` и расширение OpenAPI как источника
  правды TS-клиента/zod.
- **ADR-0003 — Модель аутентификации**: периметр fail-closed; `AUTH_DISABLED`
  только локально; внутренние ошибки наружу не раскрываются.
- **ADR-0004 — Статусы и guarded-CAS**: портал отражает статусы
  `creating/active/failed`, переходы которых выполняет уже смерженный бэкенд.
