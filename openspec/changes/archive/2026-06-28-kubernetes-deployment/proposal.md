## Why

Этапы 1–4 MVP дали работающий локальный стенд на docker-compose, но план
(docs/IDP_MVP_plan.md, «Неделя 7») требует целевого способа развёртывания
платформы в Kubernetes. Сейчас в `deploy/` нет ни Helm-чартов, ни k8s-манифестов,
ни ресурсов service mesh — единственный способ поднять систему — compose, который
не отражает прод-топологию (сетевой периметр, mTLS, раздельное масштабирование
worker'а, fail-closed-конфигурация, секреты). Без провалидированных чартов нельзя
ни закрыть Этап 5, ни обсуждать дальнейший CD.

Реального кластера в окружении нет — поэтому deliverable этого изменения — это
корректные, провалидированные (`helm lint` / `helm template` → `kubeconform` +
Istio CRD / `istioctl analyze`) чарты и манифесты плюс собираемые образы, а не
живой деплой.

## What Changes

- **Финализация образов**: подтвердить multi-stage/non-root/distroless для
  `services/{gateway,idm,projects,devinfra-worker}/Dockerfile`; перевести
  `web/Dockerfile` из dev-режима (Vite dev-сервер) в **production multi-stage**
  (`vite build` → раздача статики nginx-unprivileged, non-root) — это компонент
  «портал/Nginx» из плана. Образы-миграторы (`migrate.Dockerfile`) переиспользуются
  как Job.
- **Umbrella Helm-чарт** `deploy/helm/idp` с **library-chart** `deploy/helm/idp-lib`
  (DRY: общие шаблоны Deployment/Service/probes/securityContext/labels/ConfigMap/
  Secret/HPA). Под-релизы на компонент периметра/ядра: портал, oauth2-proxy,
  gateway, idm, projects, devinfra-worker. Окружения — через `values-<env>.yaml`
  (`local`/`prod`).
- **Рабочие нагрузки**: Deployment + Service для каждого сервиса; readiness/liveness
  probes на существующие `/readyz` (content-aware) и `/healthz`; requests/limits;
  **HPA только для devinfra-worker** (масштабируется отдельным Deployment);
  securityContext non-root/read-only-rootfs/drop-caps; миграции idm/projects как
  pre-deploy **Job** (hook).
- **Конфигурация → ConfigMap/Secret**: вычитанный из кода контракт env каждого
  сервиса разнесён на несекретный ConfigMap (адреса, namespace, TTL) и Secret
  (DSN, `KEYCLOAK_SA_CLIENT_SECRET`, токены GitLab/Vault/Harbor, `AUTH_ADMIN_KEY`,
  cookie-secret oauth2-proxy). **fail-closed**: обязательные env (`JWKS_URL` и пр.)
  в `values` так, что пустое значение роняет под; `AUTH_DISABLED`/`SSRF_DISABLED`
  отсутствуют в prod-overlay.
- **Сетевой слой на Istio** (вместо Ingress): `Gateway` (терминация внешнего TLS),
  `VirtualService` (внешний хост → oauth2-proxy → портал, `/api` → gateway,
  same-origin как в ADR-0009), `PeerAuthentication` STRICT (mTLS меша),
  `DestinationRule` (таймауты/субсеты при необходимости), `AuthorizationPolicy`
  fail-closed default-deny + явные allow между сервисами, `ServiceEntry` для
  внешних (Keycloak/Temporal/GitLab/Vault/Harbor) с сохранением app-level
  SSRF-guard как основного контроля egress.
- **Внешние stateful-зависимости** (Postgres ×2, DragonflyDB, Temporal, Keycloak,
  GitLab/Vault/Harbor) — заданы как **внешние** (конфиг подключения +
  `ServiceEntry`/`ExternalName`), полноценно не разворачиваются (границы заложены).
- **Валидация и CI**: `Makefile`-таргеты сборки образов и `helm lint`/`template`/
  `kubeconform`/`istioctl analyze`; новая CI-джоба `helm` (пинованные версии
  инструментов и Istio CRD-схем). Существующие гейты (Go/web/lint/govulncheck/
  integration) остаются зелёными.
- **ADR**: принятие Helm как стандарта упаковки деплоя и Istio как service mesh
  (топология mesh + модель секретов) — значимые инфраструктурные решения.

## Capabilities

### New Capabilities
- `kubernetes-deployment`: упаковка платформы в Helm (umbrella + library-chart),
  рабочие нагрузки/probes/HPA/securityContext, ConfigMap/Secret из values с
  fail-closed, сетевой слой Istio (Gateway/VirtualService/PeerAuthentication/
  AuthorizationPolicy/ServiceEntry), финализация образов и валидация без кластера.

### Modified Capabilities
- `ci-pipeline`: добавляется блокирующая джоба валидации чартов (`helm lint` +
  `helm template | kubeconform` с Istio CRD-схемами + `istioctl analyze`) с
  пинованными версиями инструментов.
- `local-environment`: фиксируется production-образ портала (multi-stage nginx)
  и его соотношение с dev-образом compose; артефакты деплоя в `deploy/helm`.

## Impact

- **Новый код/конфиги**: `deploy/helm/**` (чарты, шаблоны Istio, `values*.yaml`,
  `values.example`), финализация `web/Dockerfile`, новый `web/nginx.conf`.
- **Изменяемое**: `Makefile` (таргеты образов/чартов), `.github/workflows/ci.yml`
  (джоба `helm`), `docs/adr/0024..` (новые ADR).
- **Не затрагивается**: Go-бизнес-логика, контракты `.proto`/OpenAPI, поведение
  фронтенда, сгенерированный код. Реальный провижининг кластера, установка Istio
  control-plane, ArgoCD/CD и секреты в git — вне scope.
- **Безопасность**: секреты — только шаблоны/`values.example`, реальные значения и
  TLS из `deploy/tls` не коммитятся в чарты; SSRF-guard приложения остаётся
  основным контролем egress (Istio — дополнение).
