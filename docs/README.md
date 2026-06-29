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
| [0010](0010-idm-rbac-model-and-cache.md) | Модель RBAC и кэш IDM | Accepted | 2026-06-19 |
| [0011](0011-service-owners-contract-and-role-sync.md) | Контракт владельцев сервиса и синхронизация ролей | Accepted | 2026-06-19 |
| [0012](0012-decommission-semantics-and-k8s-load-check.md) | Семантика decommission (soft delete) и K8s load-check | Accepted | 2026-06-20 |
| [0013](0013-transfer-semantics-ponr-and-dual-authorization.md) | Семантика transfer: точка невозврата и двусторонняя авторизация | Accepted | 2026-06-20 |
| [0014](0014-iam-admin-authorization-and-read-contract.md) | Авторизация IAM-админки и read-контракт | Accepted | 2026-06-20 |
| [0015](0015-iam-dynamic-catalog-manage-and-system-protection.md) | Динамический каталог IAM: manage и защита системных ролей/прав | Accepted | 2026-06-21 |
| [0016](0016-iam-subject-directory-from-oidc.md) | Справочник субъектов IAM из OIDC/Keycloak | Accepted | 2026-06-21 |
| [0017](0017-portal-design-system-and-ui-architecture.md) | Дизайн-система портала и UI-архитектура | Accepted | 2026-06-21 |
| [0018](0018-e2e-authentication-path-and-workflow-determinism.md) | E2E: путь аутентификации и детерминизм workflow | Accepted | 2026-06-22 |
| [0019](0019-gitlab-auth-and-namespace-owner-mapping.md) | GitLab: auth и маппинг namespace/owner | Accepted | 2026-06-22 |
| [0020](0020-vault-auth-and-secret-engine-layout.md) | Vault: auth и раскладка secret engine | Accepted | 2026-06-23 |
| [0021](0021-harbor-auth-and-project-robot-layout.md) | Harbor: auth и раскладка project/robot | Accepted | 2026-06-27 |
| [0022](0022-portal-unified-workflow-progress-source.md) | Единый источник прогресса workflow в портале | Accepted | 2026-06-27 |
| [0023](0023-owners-required-at-service-creation.md) | Владельцы обязательны при создании сервиса | Accepted | 2026-06-27 |
| [0024](0024-helm-deployment-packaging.md) | Упаковка деплоя в Helm (umbrella + library-chart) | Accepted | 2026-06-28 |
| [0025](0025-istio-service-mesh-and-secrets.md) | Istio service mesh и нативные секреты | Accepted | 2026-06-28 |

## Статусы
Proposed · Accepted · Deprecated · Superseded · Rejected
