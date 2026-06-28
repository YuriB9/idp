## MODIFIED Requirements

### Requirement: Сквозное покрытие 4 user stories через периметр

Набор E2E ДОЛЖЕН (MUST) покрывать все четыре user stories платформы через REST-периметр (ADR-0009), вызывая операции `createService`, `getService`/`listServices`, `setServiceOwners`, `decommissionService`, `transferService` ровно так, как их видит портал. Вызовы `createService` ДОЛЖНЫ (MUST) передавать обязательный непустой набор владельцев в теле запроса (`{name, owners}`), как требует контракт периметра. Каждая user story ДОЛЖНА (MUST) иметь как минимум один happy-path-сценарий, доводящий соответствующий Temporal-workflow до терминального ожидаемого статуса.

#### Scenario: Создание сервиса с владельцами доходит до active

- **GIVEN** поднят docker-compose-стенд с моками/реальными плечами GitLab/Vault/Harbor
- **WHEN** тест вызывает `createService` для нового имени в проекте с непустым набором владельцев и поллит `getService`
- **THEN** запись фиксируется со статусом `creating` и непустым набором владельцев, затем переходит в `active` в пределах таймаут-бюджета, а Saga-activity (GitLab repo/members/CI-vars, Vault policy/AppRole, Harbor project/robot, IDM owner roles) завершаются успешно

#### Scenario: Создание без владельцев отклоняется периметром

- **GIVEN** поднят стенд
- **WHEN** тест вызывает `createService` с пустым/отсутствующим набором владельцев
- **THEN** периметр отвечает 400, запись каталога не создаётся

#### Scenario: Изменение владельцев отражается в каталоге и ролях

- **GIVEN** существует активный сервис с известной `owners_version`
- **WHEN** тест вызывает `setServiceOwners` с полным желаемым набором владельцев и текущей `owners_version`
- **THEN** `getService` возвращает обновлённый набор владельцев и инкрементированную `owners_version`, а синхронизация ролей в IDM (ADR-0011) отражает diff add/remove

#### Scenario: Decommission переводит в decommissioned (soft delete)

- **GIVEN** существует активный сервис и нагрузка снята
- **WHEN** тест вызывает `decommissionService` с `load_drained=true`
- **THEN** статус переходит в `decommissioned`, запись каталога сохраняется (не purge), доступы во внешних системах отзываются

#### Scenario: Перенос сервиса меняет проект-владельца

- **GIVEN** существует активный сервис в исходном проекте
- **WHEN** субъект с правами `transfer` и `transfer_in` вызывает `transferService` в целевой проект
- **THEN** статус проходит `active→transferring→active`, проект записи меняется на целевой, владельцы переезжают вместе с записью (ADR-0013)
