# Tasks — kubernetes-deployment

## 1. Финализация образов

- [x] 1.1 Подтвердить multi-stage/non-root/distroless и воспроизводимость 4
      Go-Dockerfile (`gateway/idm/projects/devinfra-worker`); при необходимости
      зафиксировать пин базовых образов по digest.
- [x] 1.2 Перевести `web/Dockerfile` в production multi-stage: `node:22-alpine`
      (`npm ci` + `npm run build`) → `nginxinc/nginx-unprivileged` (non-root,
      порт 8080), копировать `dist/`.
- [x] 1.3 Добавить `web/nginx.conf`: SPA-fallback `try_files … /index.html`, БЕЗ
      проксирования `/api` (комментарий — почему: в k8s `/api` маршрутит Istio).
- [x] 1.4 Проверить `docker build` всех 5 образов (зелёный, non-root).

## 2. Library-chart `deploy/helm/idp-lib`

- [x] 2.1 `Chart.yaml` (type: library), `_helpers.tpl` с `idp-lib.labels`/
      `idp-lib.selectorLabels`.
- [x] 2.2 Шаблон `idp-lib.deployment` (образ, порты, env из ConfigMap/Secret,
      probes, securityContext, ресурсы).
- [x] 2.3 Шаблоны `idp-lib.service`, `idp-lib.configmap`, `idp-lib.secret`,
      `idp-lib.hpa`, `idp-lib.probes`, `idp-lib.securityContext` (non-root,
      readOnlyRootFilesystem, drop ALL caps, no privilege escalation).
- [x] 2.4 Все комментарии шаблонов — на русском.

## 3. Umbrella-чарт `deploy/helm/idp`

- [x] 3.1 `Chart.yaml` (зависимость на `idp-lib`), `values.yaml` (базовые дефолты:
      реестр/тег образов, namespace, общие ресурсы, адреса внешних систем).
- [x] 3.2 Ресурсы 6 компонентов (портал, oauth2-proxy, gateway, idm, projects,
      devinfra-worker): Deployment+Service через `idp-lib.*`, корректные порты
      (8080/4180/8080/8081/8082/8083) и probe-пути (`/healthz`, `/readyz`).
- [x] 3.3 HPA только для devinfra-worker; ConfigMap (несекретное) и Secret
      (DSN/токены/cookie-secret/`AUTH_ADMIN_KEY`/`KEYCLOAK_SA_CLIENT_SECRET`).
- [x] 3.4 Job-миграторы idm/projects (pre-install/pre-upgrade hook,
      `sidecar.istio.io/inject:"false"`, образы `migrate.Dockerfile`).
- [x] 3.5 `values.example.yaml` — форма всех Secret-ключей с плейсхолдерами.

## 4. Istio-ресурсы (шаблоны в umbrella)

- [x] 4.1 `Gateway` (ingressgateway, терминация TLS по `credentialName`).
- [x] 4.2 `VirtualService`: внешний хост → oauth2-proxy → портал; `/api` →
      gateway (same-origin, ADR-0009).
- [x] 4.3 `PeerAuthentication` STRICT на namespace (mTLS).
- [x] 4.4 `AuthorizationPolicy` default-deny + явные allow по identity (SA) для
      требуемых пар сервисов.
- [x] 4.5 `ServiceEntry`/`ExternalName` для внешних (Keycloak/Temporal/GitLab/
      Vault/Harbor/Postgres/Dragonfly); `DestinationRule` (таймауты/TLS) по
      необходимости.

## 5. Окружения (overlay)

- [x] 5.1 `values-local.yaml`: AUTH_DISABLED/SSRF_DISABLED, http-адреса,
      ослабленный mesh (для отладки) — отражает текущую compose-семантику.
- [x] 5.2 `values-prod.yaml`: fail-closed (`JWKS_URL` через `required`, без
      `*_DISABLED`), STRICT mTLS, реальные ServiceEntry/хосты (плейсхолдеры).
- [x] 5.3 Проверить, что prod-overlay без `JWKS_URL` падает на `helm template`.

## 6. Makefile-таргеты

- [x] 6.1 Таргеты сборки образов (5 шт) с пинованными тегами.
- [x] 6.2 `helm-lint`, `helm-template` (оба overlay), `helm-validate`
      (`kubeconform` + Istio CRD-схемы), `istioctl-analyze`; версии инструментов
      и CRD-схем пинованы.
- [x] 6.3 Зонтичный таргет `helm` (lint+template+validate+analyze), комментарии
      на русском.

## 7. CI

- [x] 7.1 Джоба `helm`: установка пинованных `helm`/`kubeconform`/`istioctl`,
      lint + template(оба overlay) → kubeconform(+Istio CRD) + istioctl analyze;
      блокирующая.
- [x] 7.2 Убедиться, что существующие джобы (Go-матрица/web/lint/govulncheck/
      tidy/codegen/integration) остаются зелёными.

## 8. Документация и валидация

- [x] 8.1 ADR-0024/0025 на месте (`docs/adr/`), ссылки из чарта/values.
- [x] 8.2 Локальный прогон: `helm lint`, `helm template` (local+prod) →
      `kubeconform` (+Istio CRD) + `istioctl analyze` — зелёные.
- [x] 8.3 Все комментарии в чартах/манифестах/Dockerfile/Makefile/values — на
      русском; реальные секреты/TLS не закоммичены.
- [x] 8.4 `openspec validate kubernetes-deployment --strict` зелёный.
