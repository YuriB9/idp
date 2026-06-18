## 1. Подготовка и сверка

- [x] 1.1 Сверить scope с docs/IDP_MVP_plan.md (Этап 2 «Интеграционный слой», Этап 3 «Создание сервиса») и ADR-0001/0004/0005/0008; зафиксировать расхождения, если есть
- [x] 1.2 Создать ветку `change/create-service-workflow` от `master` (прямые коммиты в master запрещены) ДО изменения кода
- [x] 1.3 Проверить локалку docker-compose: `postgres-projects`, Temporal+UI, моки `mock-gitlab`/`mock-vault`/`mock-harbor` поднимаются, `make migrate-projects` применяет миграции

## 2. Контракт gRPC (proto) — BREAKING

- [x] 2.1 Добавить в `proto/projects/v1/projects.proto` RPC `CreateService` с `CreateServiceRequest{project,name}` и `CreateServiceResponse{id, status}`; пометить изменение **BREAKING** в комментарии (русском)
- [x] 2.2 Регенерировать стабы `make proto` (buf через `./tools`), закоммитить регенерированные файлы в `pkg/api`; убедиться, что codegen-check (git diff пуст) проходит

## 3. Общий пакет определения workflow (ADR-0008)

- [x] 3.1 Создать общий пакет под `services/projects` с определением workflow: имя workflow, типы input/output, имена activities, конструктор детерминированного `WorkflowID` (`create-service:<project>:<name>`) — импортируется и API, и worker'ом
- [x] 3.2 Реализовать тело workflow «Создание сервиса»: порядок шагов GitLab → Harbor → Vault → инъекция секретов → финальный переход статуса; `RetryPolicy`/`StartToCloseTimeout`/heartbeat на activities; детерминированный код (без I/O в workflow)
- [x] 3.3 Реализовать Saga-логику: накопление выполненных шагов с компенсациями; non-retryable `ApplicationError` → компенсации в обратном порядке; полный откат при недоступности Vault (ADR-0005); при сбое компенсации — `FAILED` + alert-маркер (структурный лог error)

## 4. Клиенты интеграций (services/devinfra-worker/internal) — Этап 2

- [x] 4.1 Объявить узкие интерфейсы клиентов GitLab/Vault/Harbor (операция провизии + парная компенсация) в `services/devinfra-worker/internal`
- [x] 4.2 HTTP-реализации поверх `pkg/httpclient` с обязательным `pkg/ssrf` (`ValidateURL` на base-URL из `GITLAB_BASE_URL`/`VAULT_BASE_URL`/`HARBOR_BASE_URL` + `GuardedDialContext` в транспорте); идемпотентность операций и компенсаций; секреты не логировать в открытом виде
- [x] 4.3 In-memory стаб клиентов, реализующий те же интерфейсы, для дефолтного прогона тестов
- [x] 4.4 WireMock-mappings для создания и удаления ресурсов GitLab/Vault/Harbor в `deploy/mocks/mappings`

## 5. Activities провизии (worker)

- [x] 5.1 Activity GitLab: создать репозиторий в группе проекта (+ компенсация удаления), идемпотентно
- [x] 5.2 Activity Harbor: создать директорию + Robot Account (+ компенсация удаления), идемпотентно
- [x] 5.3 Activity Vault: создать политики + AppRole (RoleID/SecretID) (+ компенсация удаления), идемпотентно
- [x] 5.4 Activity инъекции секретов: записать Vault RoleID/SecretID и Harbor Robot-токен в CI/CD-переменные GitLab (идемпотентно, без логирования секретов)
- [x] 5.5 Финальная activity перехода статуса через `repository.TransitionStatus` (guarded-CAS): `CREATING→ACTIVE` при успехе, `CREATING→FAILED` при фатальном сбое (`RowsAffected==0 → errs.ErrConflict`, не перетирать молча)

## 6. Регистрация worker'а и живость

- [x] 6.1 Зарегистрировать workflow «Создание сервиса» и все activities на task-queue `devinfra` в `services/devinfra-worker/main.go`; внедрить клиенты интеграций (реальные против моков) и Postgres-пул для transition-activity
- [x] 6.2 Сделать `/readyz` worker'а реальным сигналом живости (worker запущен и поллит task-queue)

## 7. Связка с каталогом (services/projects)

- [x] 7.1 Расширить `usecase` методом запуска создания: `CreateRecord` (status=CREATING) затем `ExecuteWorkflow` на task-queue `devinfra` с детерминированным `WorkflowID`; запуск только после успешной вставки; заложить границу проверки прав (заглушка IDM CheckAccess)
- [x] 7.2 Реализовать gRPC `CreateService` в `grpcapi`: вызвать usecase, вернуть `id` и `status=CREATING`; маппинг ошибок без раскрытия `err.Error()` клиенту

## 8. Тесты

- [x] 8.1 Temporal testsuite для workflow: happy-path (порядок activities, без компенсаций) — table-driven + `t.Parallel()`
- [x] 8.2 Temporal testsuite: ветки компенсаций/ретраев (мок Vault → non-retryable → компенсации Harbor+GitLab в обратном порядке; сбой компенсации → FAILED+alert; ретрай транзиентной ошибки)
- [x] 8.3 Тесты клиентов интеграций на in-memory стабе (идемпотентность операций/компенсаций) и тест блокировки SSRF-guard на запрещённом адресе
- [x] 8.4 Тесты gRPC `CreateService`/usecase (вставка CREATING до запуска workflow; запуск с детерминированным WorkflowID); goleak в пакетах с горутинами
- [x] 8.5 Integration-тесты (если есть реально-внешнее) — под тегом сборки `integration`; дефолтный прогон `go test ./...` зелёный без внешних систем

## 9. Завершение

- [x] 9.1 Прогнать локально: `go test ./...`, линтеры, `make proto` (codegen-check), миграции; обновить README/docker-compose env worker'а при необходимости
- [ ] 9.2 Открыть PR из `change/create-service-workflow`; добиться зелёного CI (матрица модулей + govulncheck + codegen-check + integration-джоб); после merge в master — `/opsx:archive`
