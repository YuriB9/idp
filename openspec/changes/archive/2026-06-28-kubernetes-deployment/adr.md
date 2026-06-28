# ADR (изменение: kubernetes-deployment)

Этот файл — индекс архитектурных решений данного изменения. Канонические ADR
лежат в `docs/adr/NNNN-kebab-title.md` (вне `openspec/`).

## Новые ADR

- **ADR-0024 — Helm (umbrella + library-chart) как стандарт упаковки деплоя**
  (`docs/adr/0024-helm-deployment-packaging.md`): платформа упаковывается одним
  umbrella-чартом `idp` поверх library-chart `idp-lib` (DRY-шаблоны), окружения —
  overlay `values-<env>.yaml`, fail-closed через Helm `required`, миграции — Job,
  worker — отдельный HPA, внешние stateful-зависимости не разворачиваются.
- **ADR-0025 — Istio service mesh для сетевого слоя и нативные Secret для MVP**
  (`docs/adr/0025-istio-service-mesh-and-secrets.md`): `Gateway`/`VirtualService`
  (терминация TLS, same-origin `/api`), `PeerAuthentication` STRICT mTLS,
  `AuthorizationPolicy` default-deny + явные allow, `ServiceEntry` для внешних с
  сохранением прикладного SSRF-guard как основного egress-контроля; секреты MVP —
  нативные k8s `Secret`.

## Реализуемые существующие ADR

- **ADR-0003 / ADR-0009 — периметр и same-origin** `/api` → gateway: модель
  сохраняется в Istio `VirtualService`.
- **ADR-0001-семейство fail-closed** (пустой `JWKS_URL` → `os.Exit(1)`,
  SSRF-guard): отражено в prod-overlay (без `*_DISABLED`) и `ServiceEntry`.
