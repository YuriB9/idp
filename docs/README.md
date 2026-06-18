# Architecture Decision Records — IDP

Записи ключевых архитектурных решений MVP. Формат — облегчённый MADR.

## Индекс

| ADR | Заголовок | Статус | Дата |
|-----|-----------|--------|------|
| [0001](0001-temporal-as-orchestrator.md) | Temporal как оркестратор провизии ресурсов | Accepted | 2026-06-18 |
| [0002](0002-grpc-internal-transport.md) | gRPC/protobuf для внутренних вызовов | Accepted | 2026-06-18 |
| [0003](0003-auth-model.md) | Модель аутентификации: Oauth2-Proxy + Keycloak + per-service JWT (fail-closed) | Accepted | 2026-06-18 |
| [0004](0004-status-transitions-guarded-cas.md) | Переходы статусов сервиса через guarded-CAS | Accepted | 2026-06-18 |
| [0005](0005-create-saga-rollback-policy.md) | Полный Saga-откат при недоступности Vault в «Создании» | Accepted | 2026-06-18 |
| [0006](0006-go-work-monorepo-layout.md) | Раскладка монорепо на go.work (модуль на сервис + изолированный tools) | Accepted | 2026-06-18 |
| [0007](0007-migration-tool-goose.md) | Инструмент миграций БД — goose | Accepted | 2026-06-18 |
| [0008](0008-workflow-definition-execution-split.md) | Разделение определения и исполнения workflow «Создание сервиса» | Accepted | 2026-06-18 |
| [0009](0009-perimeter-rest-resource-shape.md) | Форма REST-ресурсов периметра (проектно-скоупленные пути) | Accepted | 2026-06-18 |

## Статусы
Proposed · Accepted · Deprecated · Superseded · Rejected
