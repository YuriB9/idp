# ADR-0025: Istio service mesh для сетевого слоя и нативные Secret для MVP

**Status:** Accepted
**Date:** 2026-06-28
**Change:** kubernetes-deployment

## Context

При переносе платформы в Kubernetes (ADR-0024) нужно описать сетевой слой:
внешний вход и терминацию TLS, маршрутизацию периметра (портал ↔ oauth2-proxy ↔
gateway, same-origin `/api`, ADR-0003/0009), шифрование и авторизацию внутреннего
трафика, контроль исходящих вызовов к внешним системам и доставку секретов.

План MVP фиксирует **Istio** как сетевой слой (service mesh) вместо классического
Ingress. Ключевой принцип безопасности проекта (config.yaml) — fail-closed и
прикладной SSRF-guard на всех исходящих к GitLab/Vault/Harbor: сетевые средства
должны его дополнять, а не подменять.

Рассматривались для сети:
- **A. nginx-ingress + Kubernetes NetworkPolicy + ручной TLS между подами** — нет
  автоматического mTLS, нет L7-allow по identity, нет mesh-телеметрии; сложность
  сопоставима.
- **B. Istio mesh** — автоматический mTLS, L7-авторизация по identity, единая
  модель ingress/egress, телеметрия. Зафиксирован планом.

Для секретов:
- **A. External Secrets / Vault Agent Injector** — ближе к проду, но требует
  инфраструктуры за рамками MVP.
- **B. Нативные k8s Secret из values/CI** — минимально достаточно для MVP.

## Decision

Принят сетевой **вариант B (Istio)** и секретный **вариант B (нативные Secret)**.

1. **Ingress и TLS — Istio `Gateway`** на `istio-ingressgateway`: терминация
   внешнего TLS по `credentialName` (реальный TLS не коммитим; `deploy/tls` — лишь
   образец формата).

2. **Маршрутизация — `VirtualService`**: внешний хост → oauth2-proxy → портал;
   путь `/api` → gateway, сохраняя same-origin (ADR-0009) — повторяет модель
   compose/dev-прокси.

3. **mTLS — `PeerAuthentication` STRICT** на namespace: весь внутренний трафик
   шифруется и взаимно аутентифицируется.

4. **Авторизация трафика — `AuthorizationPolicy` default-deny** (пустая запрещающая
   политика на namespace) + явные allow по identity (SA) только для требуемых пар
   сервисов (портал←ingressgw, gateway←oauth2-proxy, idm←gateway/projects/worker,
   projects←gateway/worker и т.д.). Это сетевой defense-in-depth, НЕ замена
   RBAC-периметра IDM.

5. **Egress — `ServiceEntry`/`ExternalName`** для Keycloak/Temporal/GitLab/Vault/
   Harbor/Postgres/Dragonfly. **Прикладной SSRF-guard (`pkg/ssrf`) остаётся
   основным контролем исходящих** — Istio дополняет, не заменяет; `SSRF_DISABLED`
   в проде не выставляется.

6. **Sidecar-инъекция** — метка namespace `istio-injection=enabled`;
   `holdApplicationUntilProxyStarts=true` (приложение не стартует раньше sidecar:
   важно для раннего исходящего трафика). **Job-миграторы — с
   `sidecar.istio.io/inject: "false"`**, иначе под Job не завершается (sidecar не
   умирает).

7. **Секреты MVP — нативные k8s `Secret`** из `values`/CI; реальные значения вне
   git (`values.example.yaml` — плейсхолдеры). External Secrets/Vault Agent —
   отдельной задачей за пределами MVP (граница обозначена в `values`).

8. **DestinationRule** — таймауты/`connectionPool` для gRPC к idm/projects и TLS-
   настройки `ServiceEntry` при https — по необходимости.

## Consequences

- Внутренний трафик зашифрован и авторизован по identity без правок приложения;
  периметр сохраняет same-origin-контракт.
- SSRF-guard остаётся ответственным за egress-семантику — Istio не создаёт ложного
  чувства защищённости.
- Istio-нюансы (Job без sidecar, порядок старта) явно учтены в шаблонах.
- Нативные Secret — временное решение MVP; миграция на External Secrets/Vault —
  будущая задача.
- Валидация Istio-ресурсов без кластера требует пинованных Istio CRD-схем для
  `kubeconform` и `istioctl analyze` (ADR-0024, CI-джоба).
