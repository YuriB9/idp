# service-contracts Specification

## Purpose
TBD - created by archiving change foundation-and-pkg. Update Purpose after archive.
## Requirements
### Requirement: gRPC-контракты внутренних вызовов

В репозитории ДОЛЖНЫ (MUST) быть `.proto`-файлы как источник правды для внутренних вызовов gateway↔idm и gateway↔projects, с кодогеном Go-стабов. Каркас включает IDM `CheckAccess(user, resource, action)` и интерфейс сервиса проектов. Контракт `ProjectsService` ДОЛЖЕН (MUST) включать чтение каталога: `GetService` (с семантикой NotFound при отсутствии записи) и `ListServices` с keyset-пагинацией по непрозрачному курсору (поля `page_size`/`page_token` в запросе и `next_page_token` в ответе). Контракт `ProjectsService` ДОЛЖЕН (MUST) также включать `CreateService`, который стартует workflow «Создание сервиса» и возвращает идентификатор записи и текущий статус (`CREATING`). Несовместимые по wire изменения `.proto` ДОЛЖНЫ (MUST) помечаться **BREAKING**; добавление `CreateService` в этом change — **BREAKING**.

#### Scenario: Кодоген Go-стабов воспроизводим

- **WHEN** запускается генерация из `.proto` инструментами из `./tools`
- **THEN** сгенерированные Go-стабы соответствуют `.proto`, и `git diff --exit-code` после генерации пуст

#### Scenario: Изменение .proto помечается BREAKING

- **WHEN** в `.proto` вносится несовместимое по wire изменение
- **THEN** оно помечается как **BREAKING** в описании change

#### Scenario: Контракт чтения каталога присутствует

- **WHEN** инспектируется `proto/projects/v1/projects.proto`
- **THEN** `ProjectsService` содержит `GetService` и `ListServices`, а сообщения `ListServices` имеют поля keyset-курсора (`page_size`, `page_token`, `next_page_token`)

#### Scenario: Контракт создания сервиса присутствует

- **WHEN** инспектируется `proto/projects/v1/projects.proto`
- **THEN** `ProjectsService` содержит `CreateService` с запросом, несущим `(project, name)`, и ответом, несущим идентификатор записи и `ServiceStatus` (стартовое значение `SERVICE_STATUS_CREATING`)

### Requirement: OpenAPI периметра и TS-клиент

В репозитории ДОЛЖНА (MUST) быть OpenAPI-спецификация периметра (портал↔gateway)
с кодогеном TypeScript-клиента и zod-схем для `./web`. OpenAPI ДОЛЖЕН (MUST)
описывать доменные операции сценария «Создание сервиса», согласованные по форме
с gRPC-контрактом `projectsv1`:
- `POST /projects/{project}/services` с телом `{name}` — запуск создания, ответ
  с идентификатором записи и статусом `creating`;
- `GET /projects/{project}/services/{name}` — чтение статуса одного сервиса
  (`project`, `name`, `status`);
- `GET /projects/{project}/services` — листинг сервисов проекта с
  keyset-пагинацией (`page_size`/`page_token` → `next_page_token`).
Каркасный `GET /services` ДОЛЖЕН (MUST) быть заменён проектно-скоупленным
ресурсом `GET /projects/{project}/services` (**BREAKING**: меняется форма пути
и ответа). После любых правок OpenAPI кодоген ДОЛЖЕН (MUST) перегенерироваться
(`npm run gen`), а `gen:check` оставаться зелёным (пустой `git diff` по
`src/api`).

#### Scenario: Кодоген TS-клиента воспроизводим

- **WHEN** запускается генерация клиента из OpenAPI
- **THEN** сгенерированные TS-клиент и zod-схемы соответствуют спецификации, повторный запуск не даёт diff

#### Scenario: Рассинхрон контракта всплывает при валидации ответа

- **WHEN** ответ API не соответствует zod-схеме, сгенерированной из OpenAPI
- **THEN** `.parse` на стороне портала выбрасывает ошибку, делая дрейф контракта явным

#### Scenario: Доменные операции создания присутствуют в OpenAPI

- **WHEN** инспектируется `openapi/openapi.yaml`
- **THEN** в нём есть `POST /projects/{project}/services` (тело `{name}`),
  `GET /projects/{project}/services/{name}` и `GET /projects/{project}/services`
  с параметрами keyset-пагинации, а схемы ответов согласованы с `projectsv1`

#### Scenario: Каркасный листинг заменён (BREAKING)

- **WHEN** сравнивается контракт периметра до и после change
- **THEN** прежний `GET /services` отсутствует, вместо него — проектно-
  скоупленный `GET /projects/{project}/services`, и изменение помечено
  **BREAKING** в описании change

