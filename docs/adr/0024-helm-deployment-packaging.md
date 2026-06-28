# ADR-0024: Helm (umbrella + library-chart) как стандарт упаковки деплоя

**Status:** Accepted
**Date:** 2026-06-28
**Change:** kubernetes-deployment

## Context

Этап 5 MVP (docs/IDP_MVP_plan.md, «Неделя 7») требует развёртывания платформы в
Kubernetes. До сих пор единственным способом поднять систему был
`deploy/compose/docker-compose.yml` — он удобен локально, но не описывает
прод-топологию (раздельное масштабирование worker'а, fail-closed-конфигурация,
секреты, сетевой периметр). В репозитории не было ни Helm-чартов, ни голых
k8s-манифестов.

Платформа — шесть однотипных по форме рабочих нагрузок (портал, oauth2-proxy,
gateway, idm, projects, devinfra-worker): Deployment + Service + probes +
securityContext + конфигурация из env, различающиеся образом, портами, набором
probe-проверок и переменными. Нужен способ описать их без копипаста и с
параметризацией окружений (local/prod), причём deliverable — провалидированные
без живого кластера артефакты.

Рассматривались:
- **A. Набор независимых Helm-чартов на сервис** — дублирование почти идентичных
  шаблонов, рассинхрон securityContext/labels/probes, шесть отдельных релизов.
- **B. Голые манифесты + kustomize** — нет единой параметризации окружений в
  одном месте; план явно фиксирует Helm как инструмент.
- **C. Umbrella-чарт + library-chart** — общие шаблоны в библиотеке, один релиз,
  один набор `values`, overlay на окружение.

## Decision

Принят **вариант C**.

1. **Umbrella-чарт `deploy/helm/idp`** агрегирует все шесть компонентов; ставится
   одним релизом в один namespace с согласованными версиями.

2. **Library-chart `deploy/helm/idp-lib`** (тип `library`) содержит именованные
   шаблоны (`idp-lib.deployment`, `idp-lib.service`, `idp-lib.probes`,
   `idp-lib.securityContext`, `idp-lib.labels`, `idp-lib.configmap`,
   `idp-lib.secret`, `idp-lib.hpa`). Тонкие ресурсы компонентов их `include`-ят —
   никакого копипаста, единые securityContext/labels/probes.

3. **Окружения — overlay `values-<env>.yaml`.** Базовый `values.yaml` — дефолты;
   `values-local.yaml` и `values-prod.yaml` — точечные различия. Секреты НЕ
   хранятся в этих файлах; `values.example.yaml` показывает форму ключей Secret с
   плейсхолдерами, реальные значения подаются вне git (`--values`/`--set`/
   CI-secret).

4. **fail-closed в чартах.** Обязательные для прода env (`JWKS_URL` и пр.)
   рендерятся через Helm `required` — пустое значение прерывает рендер/деплой.
   `AUTH_DISABLED`/`SSRF_DISABLED` существуют только в local-overlay; деплой не
   ослабляет инвариант кода (пустой `JWKS_URL` → `os.Exit(1)`, ADR-0001-семейство),
   а отражает его.

5. **Probes на существующие эндпоинты** `pkg/httpserver`: liveness `/healthz`,
   readiness/startup — content-aware `/readyz`.

6. **Миграции idm/projects — Helm pre-deploy Job** на образах `migrate.Dockerfile`,
   до старта зависящих сервисов; Job-поды без Istio-sidecar (ADR-0025).

7. **devinfra-worker — отдельный Deployment с собственным HPA** (масштабируется
   независимо от API).

8. **Внешние stateful-зависимости** (Postgres ×2, Dragonfly, Temporal, Keycloak,
   GitLab/Vault/Harbor) чартом НЕ разворачиваются — только конфиг подключения
   (полноценный их деплой — за пределами MVP).

9. **Валидация без кластера** — `helm lint` + `helm template` (оба overlay) →
   `kubeconform` (+ Istio CRD) + `istioctl analyze`, пинованные версии; блокирующая
   CI-джоба.

## Consequences

- DRY-шаблоны: правка securityContext/probes/labels в одном месте применяется ко
  всем компонентам.
- Один релиз и один источник структуры конфигурации; различия окружений — в
  overlay, секреты — вне git.
- Появляется новая блокирующая CI-джоба и пинованные инструменты (helm/
  kubeconform/istioctl) — нагрузка на сопровождение версий (как govulncheck/lint).
- Сетевой слой (Istio) и модель секретов вынесены в ADR-0025.
