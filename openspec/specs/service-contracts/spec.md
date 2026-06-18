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

В репозитории ДОЛЖНА (MUST) быть OpenAPI-спецификация периметра (портал↔gateway) с кодогеном TypeScript-клиента и zod-схем для `./web`.

#### Scenario: Кодоген TS-клиента воспроизводим

- **WHEN** запускается генерация клиента из OpenAPI
- **THEN** сгенерированные TS-клиент и zod-схемы соответствуют спецификации, повторный запуск не даёт diff

#### Scenario: Рассинхрон контракта всплывает при валидации ответа

- **WHEN** ответ API не соответствует zod-схеме, сгенерированной из OpenAPI
- **THEN** `.parse` на стороне портала выбрасывает ошибку, делая дрейф контракта явным

