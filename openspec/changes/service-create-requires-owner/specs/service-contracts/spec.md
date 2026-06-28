## MODIFIED Requirements

### Requirement: gRPC-контракты внутренних вызовов

В репозитории ДОЛЖНЫ (MUST) быть `.proto`-файлы как источник правды для внутренних вызовов gateway↔idm и gateway↔projects, с кодогеном Go-стабов. Каркас включает IDM `CheckAccess(user, resource, action)` и интерфейс сервиса проектов. Контракт `ProjectsService` ДОЛЖЕН (MUST) включать чтение каталога: `GetService` (с семантикой NotFound при отсутствии записи) и `ListServices` с keyset-пагинацией по непрозрачному курсору (поля `page_size`/`page_token` в запросе и `next_page_token` в ответе). Контракт `ProjectsService` ДОЛЖЕН (MUST) также включать `CreateService`, который стартует workflow «Создание сервиса» и возвращает идентификатор записи и текущий статус (`CREATING`). Сообщение `CreateServiceRequest` ДОЛЖНО (MUST) нести `repeated string owners` как ОБЯЗАТЕЛЬНЫЙ набор владельцев создаваемого сервиса (минимум один непустой владелец после нормализации); создание без владельцев КОНТРАКТНО недопустимо. Несовместимые по wire изменения `.proto` ДОЛЖНЫ (MUST) помечаться **BREAKING**; добавление `CreateService` и добавление обязательного поля `owners` в `CreateServiceRequest` — **BREAKING**.

#### Scenario: Кодоген Go-стабов воспроизводим

- **WHEN** запускается генерация из `.proto` инструментами из `./tools`
- **THEN** сгенерированные Go-стабы соответствуют `.proto`, и `git diff --exit-code` после генерации пуст

#### Scenario: Изменение .proto помечается BREAKING

- **WHEN** в `.proto` вносится несовместимое по wire изменение
- **THEN** оно помечается как **BREAKING** в описании change

#### Scenario: Контракт чтения каталога присутствует

- **WHEN** инспектируется `proto/projects/v1/projects.proto`
- **THEN** `ProjectsService` содержит `GetService` и `ListServices`, а сообщения `ListServices` имеют поля keyset-курсора (`page_size`, `page_token`, `next_page_token`)

#### Scenario: Контракт создания сервиса несёт обязательных владельцев

- **WHEN** инспектируется `proto/projects/v1/projects.proto`
- **THEN** `ProjectsService` содержит `CreateService` с запросом, несущим `(project, name, repeated owners)`, и ответом, несущим идентификатор записи и `ServiceStatus` (стартовое значение `SERVICE_STATUS_CREATING`)
