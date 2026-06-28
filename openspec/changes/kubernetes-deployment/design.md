# Design — kubernetes-deployment (Этап 5 MVP)

## Context

Платформа состоит из периметра (портал-SPA, oauth2-proxy, API-шлюз `gateway`) и
ядра (`idm`, `projects` + Temporal-worker `devinfra-worker`). Локально всё
поднимается через `deploy/compose/docker-compose.yml`. Прод-целью (config.yaml,
docs/IDP_MVP_plan.md) зафиксирован Kubernetes. Helm-чартов/k8s-манифестов/Istio в
репозитории НЕТ — создаём с нуля.

Фактическое состояние, вычитанное из кода (не по памяти):

- **Порты/протоколы**: `gateway` — HTTP `:8080` (gRPC-клиент к idm/projects, не
  сервер); `idm` — gRPC `:9090` + HTTP `:8081`; `projects` — gRPC `:9090` + HTTP
  `:8082`; `devinfra-worker` — HTTP `:8083` (Temporal-worker, без gRPC-сервера);
  портал — статика; oauth2-proxy — `:4180`.
- **Probes**: `pkg/httpserver` регистрирует `/healthz` (liveness) и **content-aware
  `/readyz`** (`ReadinessChecks`): projects — Postgres ping + Temporal CheckHealth;
  idm — Postgres ping + Dragonfly ping; devinfra-worker — флаг `alive` (worker
  поллит task-queue); gateway — состояние gRPC-соединений к idm/projects.
- **Контракт env** (из `config.String/Int/Bool/Duration`, `auth.*`):
  - общий: `LOG_LEVEL`, `HTTP_ADDR`.
  - auth (`pkg/auth`, fail-closed): `JWKS_URL` (форсится https; пустой при
    включённой auth → `os.Exit(1)`), `AUTH_DISABLED`(+`AUTH_DISABLED_SUBJECT`),
    `AUTH_ISSUER`, `AUTH_AUDIENCE`, `AUTH_METHODS`, `AUTH_ADMIN_KEY`.
  - gateway: `IDM_GRPC_ADDR`, `PROJECTS_GRPC_ADDR`.
  - idm: `GRPC_ADDR`, `PG_DSN`, `REDIS_ADDR`, `IDM_CACHE_TTL[_DENY]`,
    `IDM_IDENTITY_CACHE_TTL`, `SSRF_DISABLED`, `KEYCLOAK_BASE_URL/REALM/
    SA_CLIENT_ID/SA_CLIENT_SECRET`.
  - projects: `GRPC_ADDR`, `PG_DSN`, `PG_MAX_CONNS`, `IDM_GRPC_ADDR`,
    `TEMPORAL_HOSTPORT/NAMESPACE/TASK_QUEUE`.
  - devinfra-worker: `PG_DSN`, `PG_MAX_CONNS`, `IDM_GRPC_ADDR`, `TEMPORAL_*`,
    `GITLAB/VAULT/HARBOR_BASE_URL`, `GITLAB_OWNER_LOGINS`, `HARBOR_USERNAME` +
    токен/файл, `SSRF_DISABLED`.
- **Образы**: 4 Go-Dockerfile — multi-stage `golang:1.26.4` → `distroless/static
  -debian12:nonroot`, `GOWORK=off`, `CGO_ENABLED=0`, `-trimpath`. `web/Dockerfile`
  — пока DEV (Vite dev-сервер `:3000`), требует перевода в production. Есть
  `migrate.Dockerfile` для idm/projects.
- **Перимиетр** (compose + oauth2-proxy.cfg, ADR-0003/0009): внешний вход →
  oauth2-proxy (`:4180`, keycloak-oidc) → upstream `gateway:8080`; портал
  same-origin `/api` → gateway.

## Goals / Non-Goals

**Goals**
- Провалидированные без кластера Helm-чарты для всех 6 рабочих нагрузок + Istio.
- Финализированные воспроизводимые образы (включая production-портал).
- fail-closed конфигурация в prod-overlay; секреты только шаблонами.
- Зелёная CI-джоба валидации чартов; остальные гейты не ломаются.

**Non-Goals**
- Живой деплой, провижининг кластера/облака, установка Istio control-plane,
  ArgoCD/CD-пайплайн.
- Полноценный деплой Postgres/Dragonfly/Temporal/Keycloak/GitLab/Vault/Harbor
  (только границы и конфиг подключения).
- External Secrets/Vault Agent Injector (за пределами MVP — нативные Secret).
- Изменения бизнес-логики/контрактов/фронтенд-поведения.

## Decisions

### 1. Umbrella-чарт + library-chart (а не набор независимых чартов)
`deploy/helm/idp` (umbrella) агрегирует компоненты; общие шаблоны вынесены в
`deploy/helm/idp-lib` (тип `library`). Под каждый компонент — каталог `templates/`
с тонкими ресурсами, которые `include`-ят именованные шаблоны библиотеки
(`idp-lib.deployment`, `idp-lib.service`, `idp-lib.probes`, `idp-lib.securityContext`,
`idp-lib.labels`).

- **Почему**: 6 почти однотипных Deployment/Service отличаются образом, портами,
  env и probe-набором. Library-chart убирает копипаст и даёт единые
  securityContext/labels/probes. Umbrella даёт один `helm install`/один набор
  `values`, общий namespace и согласованные версии.
- **Альтернативы**: (а) 6 отдельных чартов — дублирование шаблонов, рассинхрон;
  (б) голые манифесты + kustomize — нет параметризации окружений в одном месте,
  плановое требование — именно Helm. Отклонены.

### 2. Окружения через `values-<env>.yaml` overlay
Базовый `values.yaml` — общие дефолты; `values-local.yaml` (AUTH_DISABLED=true,
http-моки, без mTLS-строгости/одиночный namespace) и `values-prod.yaml`
(fail-closed: реальный `JWKS_URL`, без `*_DISABLED`, STRICT mTLS, ServiceEntry).
Секретные значения — НЕ в этих файлах; `values.example.yaml` показывает форму
Secret-ключей с плейсхолдерами.

- **Почему**: один источник структуры, разница окружений — точечными overlay;
  совпадает с принятой практикой проекта (e2e-overlay в compose).

### 3. fail-closed конфигурации в чартах
Обязательные env рендерятся из `values` через `required` (Helm-функция), так что
пустое значение в prod ломает рендер/деплой, а не молча проходит. `AUTH_DISABLED`
и `SSRF_DISABLED` существуют ТОЛЬКО в `values-local`; в prod-overlay их нет
(приложение само форсит fail-closed: пустой `JWKS_URL` → `os.Exit(1)`). Деплой не
ослабляет инвариант кода, а отражает его.

### 4. Probes на существующие эндпоинты
`livenessProbe` → `GET /healthz`; `readinessProbe` → `GET /readyz` (content-aware,
уже реализован). Стартовый лаг (gRPC-коннекты, Temporal) покрывается `startupProbe`
с тем же `/readyz`. Порт probe — HTTP-порт сервиса (8080/8081/8082/8083).

### 5. devinfra-worker — отдельный Deployment + HPA
Воркер масштабируется независимо от API. Отдельный Deployment, свой HPA (CPU
target), `readinessProbe` снимает трафик при остановке поллинга. Остальные
сервисы — фиксированные реплики (HPA опционален, по умолчанию выкл).

### 6. Сетевой слой — Istio mesh (а не Ingress + NetworkPolicy)
- **Gateway** на `istio-ingressgateway`: терминация внешнего TLS (секрет
  `credentialName`, реальный TLS не коммитим; `deploy/tls` — лишь образец формата).
- **VirtualService**: внешний хост → oauth2-proxy; `/api` → gateway сохраняя
  same-origin (ADR-0009); портал-статика — корень. Модель периметра
  (портал↔oauth2-proxy↔gateway) повторяет compose/dev-прокси.
- **PeerAuthentication** STRICT на namespace — mTLS всего внутреннего трафика.
- **AuthorizationPolicy**: пустая default-deny на namespace (fail-closed) + явные
  allow по `principals` (SA каждого сервиса): портал←ingressgw, gateway←oauth2-proxy,
  idm←gateway/projects/worker, projects←gateway/worker, и т.д. Это сетевой
  defense-in-depth, НЕ замена RBAC-периметра.
- **DestinationRule**: таймауты/`connectionPool` для gRPC к idm/projects при
  необходимости.
- **ServiceEntry** (+ `DestinationRule` TLS при https) для внешних
  Keycloak/Temporal/GitLab/Vault/Harbor/Postgres/Dragonfly. **app-level SSRF-guard
  остаётся основным egress-контролем** — Istio дополняет, не заменяет.
- **Sidecar-инъекция**: namespace-метка `istio-injection=enabled`;
  `holdApplicationUntilProxyStarts=true` чтобы приложение не стартовало раньше
  sidecar (важно для миграционных Job и раннего исходящего трафика). Job-миграторы
  — с отключённым sidecar (`sidecar.istio.io/inject: "false"`), иначе Job не
  завершается (sidecar не умирает) — это известный нюанс.

- **Альтернатива**: nginx-ingress + Kubernetes NetworkPolicy + ручной TLS между
  подами. Даёт меньше (нет автоматического mTLS, нет L7-allow по identity, нет
  телеметрии меша) при сравнимой сложности. План явно требует Istio.

### 7. Секреты — нативные k8s Secret из values/CI (MVP)
Secret-манифесты рендерятся из `values` (ключи-плейсхолдеры в `values.example`),
реальные значения подаются вне git (`--set`/`--values` приватный/CI-secret).
External Secrets/Vault Agent — отдельной задачей за MVP (граница обозначена в
`values` и комментариях).

### 8. Внешние stateful-зависимости
Postgres ×2, Dragonfly, Temporal, Keycloak, GitLab/Vault/Harbor — НЕ
разворачиваются чартом. Адреса — в ConfigMap/Secret; доступ из меша — через
`ServiceEntry`/`ExternalName`. Это соответствует scope «границы, не деплой».

### 9. Production-образ портала
`web/Dockerfile` → multi-stage: `node:22-alpine` (`npm ci` + `vite build`) →
`nginxinc/nginx-unprivileged` (non-root, порт 8080) со статикой `dist/` и
`web/nginx.conf` (SPA-fallback `try_files … /index.html`; БЕЗ проксирования `/api`
— в k8s `/api` маршрутизирует Istio VirtualService на gateway, в отличие от
dev-Vite-прокси). Dev-образ для compose сохраняется отдельно (не ломаем локалку).

## Risks / Trade-offs

- **Нет кластера для проверки рантайма** → опираемся на `helm template` +
  `kubeconform` (со схемами Istio CRD) + `istioctl analyze`; покрываем оба overlay
  (local/prod) в CI, чтобы ловить ошибки рендера/схемы статически.
- **Istio sidecar ломает Job-миграторы** (под не завершается) → `inject:"false"`
  на Job; задокументировано в шаблоне.
- **Sidecar и порядок старта/probes** → `holdApplicationUntilProxyStarts`;
  readiness уже content-aware, не отдаёт трафик до готовности зависимостей.
- **Версии Istio CRD-схем дрейфуют** → пинуем версию схем kubeconform и
  `istioctl`; фиксируем в Makefile/CI (воспроизводимость, как govulncheck/lint).
- **Случайный коммит реальных секретов/TLS** → в чартах только `values.example`
  и `credentialName`-ссылки; `.gitignore`/ревью; `deploy/tls` не копируется.
- **Расхождение dev- и prod-портала** (Vite-прокси `/api` vs Istio-маршрут) →
  явно задокументировано; same-origin сохранён в обоих.

## Migration Plan

Изменение не трогает рантайм существующих сред (compose-локалка работает как
прежде). Раскатка:
1. Финализировать образы (`web` → prod multi-stage), убедиться `docker build`
   зелёный для всех 5.
2. Добавить `idp-lib` + `idp` чарты и Istio-шаблоны; `helm lint`/`template`/
   `kubeconform`/`istioctl analyze` локально по обоим overlay.
3. Добавить Makefile-таргеты и CI-джобу `helm`.
4. ADR на Helm/Istio.

Откат: чарты аддитивны; удаление `deploy/helm/**` и CI-джобы возвращает к
прежнему состоянию без последствий для кода/compose.

## Open Questions

- Точный публичный хост и `credentialName` TLS-секрета для prod-Gateway —
  плейсхолдеры в `values-prod` (закрывается при наличии реального кластера/домена).
- Нужны ли `ServiceMonitor`/scrape-аннотации для Prometheus — Istio даёт
  телеметрию меша; добавляем только аннотации scrape для app-метрик, без
  дублирования mesh-метрик (минимально, по месту).
- Полноценный деплой Temporal/Keycloak/Postgres — отдельная инфра-задача (вне
  MVP); сейчас только конфиг подключения/ServiceEntry.
