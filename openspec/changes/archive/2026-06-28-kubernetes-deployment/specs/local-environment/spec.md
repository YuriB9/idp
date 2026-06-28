## ADDED Requirements

### Requirement: Артефакты Kubernetes-деплоя и production-образ портала
Репозиторий SHALL содержать артефакты развёртывания в Kubernetes под
`deploy/helm/` (umbrella-чарт `idp`, library-chart `idp-lib`, `values-<env>.yaml`,
`values.example.yaml`). Портал SHALL иметь воспроизводимый production-образ
(`web/Dockerfile`, multi-stage `vite build` → nginx-unprivileged, non-root),
отдельный от dev-образа compose; локальный compose-стенд SHALL продолжать
работать без изменений поведения.

#### Scenario: артефакты деплоя присутствуют и не ломают локалку
- **WHEN** в репозитории есть `deploy/helm/**` и финализированный `web/Dockerfile`
- **THEN** `deploy/compose/docker-compose.yml` поднимает локальный стенд как
  прежде, а production-образ портала собирается отдельно

#### Scenario: dev- и prod-портал сохраняют same-origin /api
- **WHEN** портал обращается к `/api`
- **THEN** в dev (Vite) запрос проксируется на gateway, в Kubernetes — маршрутится
  Istio на gateway, в обоих случаях same-origin (ADR-0009)
