## 1. Подготовка ветки

- [x] 1.1 Создать ветку `change/devinfra-real-gitlab` от актуального master (прямые коммиты в master запрещены)
- [x] 1.2 Убедиться, что дефолтный прогон зелёный до изменений (`make test`, `make lint`, `make gen` без диффа)

## 2. GitLab-клиент: аутентификация

- [x] 2.1 Добавить поле `GitLabToken string` в `integrations.Config` (комментарий на русском)
- [x] 2.2 Прокинуть токен в `gitLabHTTP` (через `doer` либо поле структуры) и ставить заголовок `PRIVATE-TOKEN` ТОЛЬКО для GitLab-запросов; токен не логировать
- [x] 2.3 В `devinfra-worker/main.go` читать env `GITLAB_TOKEN` (`config.String`) и передавать в `Config`
- [x] 2.4 Юнит-тест на стаб-сервере: GitLab-запрос несёт `PRIVATE-TOKEN`, Vault/Harbor — нет

## 3. GitLab-клиент: резолвинг идентификаторов

- [x] 3.1 Реализовать резолвинг пути группы → `namespace_id` (`GET /api/v4/groups/:path`) с кэшем в процессе (мьютекс), использовать в `CreateRepo` (`namespace_id` число)
- [x] 3.2 Реализовать резолвинг логина владельца → `user_id` (`GET /api/v4/users?username=`) и фикстуру-маппинг UUID→логин; `SyncMembers`/`RestoreMembers` шлют `user_id` + числовой `access_level`
- [x] 3.3 Незаданный маппинг владельца пропускать без ошибки воркфлоу (warn, не fail)
- [x] 3.4 Перепроверить URL-кодирование пути `project%2Fname` на archive/unarchive/transfer/variables
- [x] 3.5 Юнит-тесты на стаб-сервере: тела запросов содержат `namespace_id`/`user_id` (числа), пропуск незаданного владельца

## 4. GitLab-клиент: идемпотентность под реальный API

- [x] 4.1 `CreateRepo`: GET-then-create (проверка существования по пути → no-op-успех), убрать опору на 409
- [x] 4.2 `TransferRepo`: проверка текущего namespace → no-op-успех при уже целевой группе
- [x] 4.3 `DeleteRepo`: трактовать `404`/`202` как успех
- [x] 4.4 `InjectVariables`: перейти на upsert `PUT /projects/:id/variables/:key`; значения не логировать
- [x] 4.5 `Archive`/`Unarchive`: подтвердить идемпотентные коды реального GitLab
- [x] 4.6 Юнит-тесты идемпотентности на стаб-сервере (повторный create/transfer/inject — no-op-успех)

## 5. Выбор реализации клиента в рантайме

- [x] 5.1 В `main.go`: при заданном `GITLAB_TOKEN` собирать GitLab-клиент против реального GitLab, иначе — против моков (Vault/Harbor всегда моки)
- [x] 5.2 Подтвердить, что без токена дефолтное поведение (моки) и in-memory стаб не изменились
- [x] 5.3 Прогнать `make test` и `make lint` — зелёные; `make gen` без диффа (gen:check)

## 6. Стенд: реальный GitLab CE в отдельном override

- [x] 6.1 Создать `deploy/compose/docker-compose.gitlab.yml` (override, образ `gitlab/gitlab-ce` пиннут по тегу, healthcheck `/-/health` или `/-/readiness`, комментарии на русском)
- [x] 6.2 Переключить `devinfra-worker` в override на реальный GitLab (`GITLAB_BASE_URL`, `GITLAB_TOKEN`, `SSRF_DISABLED=true`, `depends_on: gitlab service_healthy`)
- [x] 6.3 Детерминированный сид: root PAT фиксированным значением фикстуры (env образа / init `rails runner` / seed-контейнер)
- [x] 6.4 Сид: предсоздать группы `demo`/`demo2` и тест-пользователей под владельцев
- [x] 6.5 Проверить, что дефолтная локалка, e2e-override и conformance-таргет НЕ затронуты

## 7. Makefile: цели локального прогона

- [x] 7.1 Добавить `COMPOSE_GITLAB` и цели `gitlab-up`/`gitlab-test`/`gitlab-down`/`gitlab` по образцу `e2e-*` (прогон только локальный, не в CI)
- [x] 7.2 Добавить gate-env (`GITLAB_API_URL`, `GITLAB_TOKEN`, `GITLAB_STATUS_TIMEOUT`) в `gitlab-test`

## 8. Интеграционные тесты против реального GitLab

- [x] 8.1 Новый файл в `tests/e2e` (тег `integration`) с `requireGitLab` (skip без gate-env), переиспользующий харнесс (токен, `callAPI`, `waitForStatus`, `uniqueName`)
- [x] 8.2 GitLab-API клиент в тесте: проверка существования репо в группе, CI-переменных, `archived`, namespace после transfer
- [x] 8.3 Тест создания: воркфлоу→`active`, ассерт репо в группе `demo` + переменные заданы
- [x] 8.4 Тест decommission: воркфлоу→`decommissioned`, ассерт `archived=true`
- [x] 8.5 Тест transfer: воркфлоу→`active` в `demo2`, ассерт репо в `demo2` и отсутствует в `demo`
- [ ] 8.6 (Опц.) Наблюдение компенсации `DeleteRepo` при инъецированном сбое создания (ADR-0005)
- [x] 8.7 Подтвердить: без gate-env набор пропускается, `make e2e` (моки) не затронут; компиляция пакета с `-tags=integration` зелёная

## 9. Завершение

- [x] 9.1 Финальная сверка: `make test` (race+shuffle), `make lint`, `govulncheck`, `make gen` без диффа, `make lint-openapi`, web-test — зелёные
- [x] 9.2 Локально прогнать `make gitlab` и убедиться, что интеграционный набор проходит против реального GitLab
- [x] 9.3 Открыть PR с зелёным CI; после merge — `/opsx:archive` отдельным PR sync+archive (по образцу #55/#57)
