## ADDED Requirements

### Requirement: docker-compose локалка платформы

`docker-compose` ДОЛЖЕН (MUST) поднимать полный локальный стенд: Keycloak, Oauth2-Proxy, два экземпляра PostgreSQL (для projects и idm), DragonflyDB, Temporal Server + UI, скелеты сервисов (gateway, idm, projects, devinfra-worker) и моки внешних систем GitLab/Vault/Harbor.

#### Scenario: Полный стенд поднимается одной командой

- **WHEN** выполняется `docker compose up`
- **THEN** стартуют все перечисленные компоненты, и сервисы-скелеты становятся доступны для проверки health-эндпоинтов

#### Scenario: Отдельные базы для projects и idm

- **WHEN** инспектируется конфигурация compose
- **THEN** заданы два независимых PostgreSQL-инстанса — для каталога проектов и для IDM

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
