# kubernetes-deployment Specification

## Purpose
Развёртывание платформы IDP в Kubernetes: упаковка в Helm (umbrella +
library-chart), рабочие нагрузки/probes/HPA/securityContext, ConfigMap/Secret
из values с fail-closed, сетевой слой Istio (Gateway/VirtualService/
PeerAuthentication/AuthorizationPolicy/ServiceEntry), финализация образов и
валидация без кластера (ADR-0024/0025).

## Requirements
### Requirement: Helm umbrella-чарт с library-chart
Платформа SHALL упаковываться в umbrella Helm-чарт `deploy/helm/idp`, который
агрегирует рабочие нагрузки периметра и ядра (портал, oauth2-proxy, gateway, idm,
projects, devinfra-worker). Общие шаблоны (Deployment, Service, probes,
securityContext, labels, ConfigMap, Secret, HPA) SHALL выноситься в
library-chart `deploy/helm/idp-lib` (тип `library`) и переиспользоваться через
`include`, без копипаста на компонент. Чарты SHALL проходить `helm lint` без
ошибок.

#### Scenario: helm lint umbrella-чарта
- **WHEN** выполняется `helm lint deploy/helm/idp` с любым из `values-<env>.yaml`
- **THEN** команда завершается успешно без ошибок и без необъявленных значений

#### Scenario: библиотечные шаблоны переиспользуются
- **WHEN** рендерится Deployment любого из шести компонентов
- **THEN** он собран через именованные шаблоны `idp-lib.*` (а не продублирован),
  и все компоненты получают единые labels и securityContext

### Requirement: Рабочие нагрузки с probes, ресурсами и securityContext
Каждый сервис SHALL разворачиваться как Deployment + Service. `livenessProbe`
SHALL указывать на `GET /healthz`, `readinessProbe` (и `startupProbe`) — на
content-aware `GET /readyz` на HTTP-порту сервиса (gateway 8080, idm 8081,
projects 8082, devinfra-worker 8083). Поды SHALL запускаться non-root
(`runAsNonRoot: true`), с `readOnlyRootFilesystem`, `allowPrivilegeEscalation:
false` и сброшенными Linux-capabilities, и SHALL объявлять requests/limits CPU и
памяти.

#### Scenario: probes ссылаются на существующие эндпоинты
- **WHEN** рендерится Deployment сервиса
- **THEN** liveness указывает на `/healthz`, readiness и startup — на `/readyz`
  на корректном HTTP-порту этого сервиса

#### Scenario: контейнер не может выполняться от root
- **WHEN** рендерится spec пода любого сервиса
- **THEN** `securityContext` запрещает root и privilege escalation и сбрасывает
  capabilities

### Requirement: Отдельное масштабирование devinfra-worker
`devinfra-worker` SHALL разворачиваться отдельным Deployment с собственным
HorizontalPodAutoscaler (масштабирование по CPU), независимым от API-сервисов.
Готовность пода SHALL сниматься, когда worker прекращает поллинг Temporal
task-queue (через существующую `/readyz`-проверку `alive`).

#### Scenario: HPA только у воркера
- **WHEN** рендерится чарт
- **THEN** HPA создаётся для devinfra-worker и нацелен на его Deployment, а
  API-сервисы по умолчанию имеют фиксированные реплики без HPA

### Requirement: Конфигурация через ConfigMap и Secret из values
Несекретная конфигурация SHALL рендериться в ConfigMap (адреса сервисов/внешних
систем, namespace, TTL, log-level), а секреты — в Secret (`PG_DSN`,
`KEYCLOAK_SA_CLIENT_SECRET`, токены GitLab/Vault/Harbor, `AUTH_ADMIN_KEY`,
cookie-secret oauth2-proxy). Реальные значения секретов и TLS НЕ SHALL
коммититься; репозиторий SHALL содержать только `values.example.yaml` с
плейсхолдерами и ссылки `credentialName`.

#### Scenario: секреты разнесены и не содержат реальных значений
- **WHEN** просматриваются файлы чарта в репозитории
- **THEN** секретные ключи присутствуют только как плейсхолдеры в
  `values.example.yaml`, а реальных секретов/приватных ключей TLS в `deploy/helm`
  нет

### Requirement: fail-closed конфигурация в prod-overlay
Окружения SHALL задаваться через `values-<env>.yaml`. Обязательные для прода env
(в частности `JWKS_URL`) SHALL рендериться через Helm `required`, так что пустое
значение прерывает рендер/деплой. `AUTH_DISABLED` и `SSRF_DISABLED` SHALL
присутствовать ТОЛЬКО в локальном overlay и отсутствовать в prod-overlay.

#### Scenario: пустой JWKS_URL в prod ломает рендер
- **WHEN** `helm template` с prod-overlay вызывается без заданного `JWKS_URL`
- **THEN** рендер завершается ошибкой `required`, а не выпускает под с пустым
  значением

#### Scenario: в prod нет байпасов аутентификации
- **WHEN** рендерится prod-overlay
- **THEN** ни в одном манифесте нет `AUTH_DISABLED` или `SSRF_DISABLED`

### Requirement: Миграции БД как pre-deploy Job
Схемы idm и projects SHALL применяться Job'ами (на образах `migrate.Dockerfile`)
до старта зависящих сервисов (Helm pre-install/pre-upgrade hook). Поды Job SHALL
запускаться без Istio-sidecar (`sidecar.istio.io/inject: "false"`), чтобы Job
корректно завершался.

#### Scenario: миграция выполняется до сервиса и без sidecar
- **WHEN** рендерится чарт
- **THEN** для idm и projects созданы Job-миграторы с hook-аннотацией pre-deploy
  и отключённой инъекцией sidecar

### Requirement: Сетевой периметр и mTLS на Istio
Внешний трафик SHALL входить через Istio `Gateway` (терминация TLS по
`credentialName`) и маршрутизироваться `VirtualService`: внешний хост →
oauth2-proxy → портал, путь `/api` → gateway (same-origin, ADR-0009).
Внутренний трафик меша SHALL шифроваться `PeerAuthentication` в режиме STRICT.

#### Scenario: маршрутизация периметра сохраняет same-origin
- **WHEN** рендерится VirtualService
- **THEN** `/api` направляется на gateway, остальной трафик — на портал/oauth2-proxy
  под одним внешним хостом

#### Scenario: внутренний mTLS строгий
- **WHEN** рендерится чарт
- **THEN** на namespace задан `PeerAuthentication` с `mtls.mode: STRICT`

### Requirement: fail-closed авторизация трафика и контролируемый egress
Namespace SHALL иметь Istio `AuthorizationPolicy` по умолчанию запрещающую весь
трафик (default-deny) с явными allow-правилами между конкретными сервисами по их
identity. Доступ к внешним системам (Keycloak, Temporal, GitLab, Vault, Harbor,
Postgres, Dragonfly) SHALL описываться `ServiceEntry`/`ExternalName`. Istio НЕ
SHALL подменять прикладной SSRF-guard — он остаётся основным контролем исходящих
вызовов.

#### Scenario: запрет по умолчанию плюс явные allow
- **WHEN** рендерится чарт
- **THEN** присутствует default-deny `AuthorizationPolicy` и явные allow только
  для требуемых пар сервисов

#### Scenario: внешние зависимости объявлены и SSRF-guard сохранён
- **WHEN** рендерится prod-overlay
- **THEN** для внешних систем есть `ServiceEntry`, а конфигурация сервисов НЕ
  выставляет `SSRF_DISABLED`

### Requirement: Внешние stateful-зависимости не разворачиваются чартом
Postgres (×2), DragonflyDB, Temporal, Keycloak, GitLab, Vault и Harbor SHALL
конфигурироваться как внешние (адреса подключения в ConfigMap/Secret, доступ из
меша через `ServiceEntry`/`ExternalName`) и НЕ SHALL разворачиваться umbrella-чартом.

#### Scenario: чарт не содержит stateful-нагрузок
- **WHEN** рендерится umbrella-чарт
- **THEN** он не создаёт Deployment/StatefulSet для Postgres/Dragonfly/Temporal/
  Keycloak/GitLab/Vault/Harbor, а только конфиг подключения к ним

### Requirement: Воспроизводимый production-образ портала
`web/Dockerfile` SHALL собирать портал многоэтапно (`vite build` → раздача
статики nginx-unprivileged) и запускаться non-root. SPA SHALL отдаваться с
fallback на `index.html`; маршрутизация `/api` в Kubernetes выполняется Istio (не
встроенным прокси образа). Образы всех сервисов SHALL собираться воспроизводимо.

#### Scenario: docker build портала зелёный и non-root
- **WHEN** выполняется `docker build` для `web/Dockerfile`
- **THEN** сборка успешна, итоговый образ раздаёт статику от non-root пользователя

### Requirement: Валидация чартов без кластера
Чарты и Istio-ресурсы SHALL валидироваться без живого кластера: `helm lint`,
`helm template` для каждого overlay с проверкой вывода через `kubeconform`
(включая схемы Istio CRD) и `istioctl analyze`. Версии инструментов и CRD-схем
SHALL быть пинованы для воспроизводимости.

#### Scenario: рендер обоих overlay проходит kubeconform
- **WHEN** `helm template` с local- и prod-overlay прогоняется через `kubeconform`
  со схемами Istio CRD
- **THEN** валидация проходит без ошибок схемы для обоих окружений
