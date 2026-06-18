# local-environment Specification

## Purpose
TBD - created by archiving change foundation-and-pkg. Update Purpose after archive.
## Requirements
### Requirement: docker-compose локалка платформы

`docker-compose` ДОЛЖЕН (MUST) поднимать полный локальный стенд: Keycloak,
Oauth2-Proxy, два экземпляра PostgreSQL (для projects и idm), DragonflyDB,
Temporal Server + UI, скелеты сервисов (gateway, idm, projects,
devinfra-worker), моки внешних систем GitLab/Vault/Harbor и портал (`./web`).
Стенд ДОЛЖЕН (MUST) обеспечивать сквозную визуальную проверку сценария
«Создание сервиса»: портал → gateway(REST) → projects(gRPC) → Temporal →
DevInfra worker (моки), с наблюдаемым переходом статуса `CREATING`→`ACTIVE`.
Портал ДОЛЖЕН (MUST) быть доступен в браузере (сервис `web` в compose ИЛИ явно
задокументированный `npm run dev` на `:3000` с прокси `/api` на gateway
`:8081`), а локальная аутентификация (`AUTH_DISABLED` у gateway / oauth2-proxy)
и CORS — согласованы так, чтобы запросы портала доходили до периметра.

#### Scenario: Полный стенд поднимается одной командой

- **WHEN** выполняется `docker compose up`
- **THEN** стартуют все перечисленные компоненты, и сервисы-скелеты становятся доступны для проверки health-эндпоинтов

#### Scenario: Отдельные базы для projects и idm

- **WHEN** инспектируется конфигурация compose
- **THEN** заданы два независимых PostgreSQL-инстанса — для каталога проектов и для IDM

#### Scenario: Портал доступен и проксирует периметр

- **GIVEN** поднятый локальный стенд
- **WHEN** открывается портал в браузере и отправляется доменный запрос на `/api/...`
- **THEN** запрос доходит до gateway (через прокси/oauth2-proxy) без CORS-ошибок,
  и портал получает ответ периметра

#### Scenario: Сквозное создание сервиса наблюдается визуально

- **GIVEN** поднятый стенд с моками GitLab/Vault/Harbor и DevInfra worker
- **WHEN** через портал создаётся сервис
- **THEN** статус в портале меняется `creating`→`active`, что кросс-проверяется в
  Temporal UI (`localhost:8080`) и логах worker'а

### Requirement: Локальная аутентификация через Keycloak + Oauth2-Proxy

Локалка ДОЛЖНА (MUST) включать преднастроенный Keycloak realm (клиент портала, базовые роли) и Oauth2-Proxy перед gateway, чтобы OIDC-поток периметра проверялся end-to-end локально.

#### Scenario: Трафик к gateway идёт через Oauth2-Proxy

- **WHEN** запрос портала направляется на периметр
- **THEN** он проходит через Oauth2-Proxy с OIDC-аутентификацией в Keycloak, и только авторизованный трафик доходит до gateway

### Requirement: Моки управляемых систем

Локалка ДОЛЖНА (MUST) предоставлять mock-серверы GitLab, Vault и Harbor, чтобы DevInfra worker и сервисы могли работать против контрактных заглушек без реальных внешних систем.

#### Scenario: Скелеты обращаются к мокам, а не к реальным системам

- **WHEN** компонент в локалке обращается к GitLab/Vault/Harbor
- **THEN** запрос направляется на соответствующий mock-сервер из compose

