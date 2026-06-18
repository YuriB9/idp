## MODIFIED Requirements

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
