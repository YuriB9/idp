import { makeApi, Zodios, type ZodiosOptions } from "@zodios/core";
import { z } from "zod";

const HealthStatus = z.object({ status: z.literal("ok") }).passthrough();
const ServiceStatus = z.enum([
  "creating",
  "active",
  "decommissioned",
  "failed",
  "transferring",
]);
const ServiceSummary = z
  .object({
    project: z.string(),
    name: z.string(),
    status: ServiceStatus,
    owners: z.array(z.string()),
    owners_version: z.number().int(),
    decommissioned_at: z.string().optional(),
  })
  .passthrough();
const ServiceList = z
  .object({ services: z.array(ServiceSummary), next_page_token: z.string() })
  .passthrough();
const Error = z.object({ error: z.string() }).passthrough();
const CreateServiceRequest = z
  .object({ name: z.string().min(1) })
  .passthrough();
const CreateServiceResult = z
  .object({ id: z.string(), status: ServiceStatus })
  .passthrough();
const DecommissionServiceRequest = z
  .object({ load_drained: z.boolean() })
  .passthrough();
const SetServiceOwnersRequest = z
  .object({
    owners: z.array(z.string().min(1)),
    owners_version: z.number().int(),
  })
  .passthrough();
const SetServiceOwnersResult = z
  .object({ owners: z.array(z.string()), owners_version: z.number().int() })
  .passthrough();
const TransferServiceRequest = z
  .object({ target_project: z.string().min(1) })
  .passthrough();
const Role = z.object({ name: z.string() }).passthrough();
const RoleList = z.object({ roles: z.array(Role) }).passthrough();
const Permission = z
  .object({ action: z.string(), resource: z.string() })
  .passthrough();
const PermissionList = z
  .object({ permissions: z.array(Permission) })
  .passthrough();
const SubjectRoles = z
  .object({ subject: z.string(), roles: z.array(z.string()) })
  .passthrough();
const SubjectList = z
  .object({ subjects: z.array(SubjectRoles), next_page_token: z.string() })
  .passthrough();

export const schemas = {
  HealthStatus,
  ServiceStatus,
  ServiceSummary,
  ServiceList,
  Error,
  CreateServiceRequest,
  CreateServiceResult,
  DecommissionServiceRequest,
  SetServiceOwnersRequest,
  SetServiceOwnersResult,
  TransferServiceRequest,
  Role,
  RoleList,
  Permission,
  PermissionList,
  SubjectRoles,
  SubjectList,
};

const endpoints = makeApi([
  {
    method: "get",
    path: "/healthz",
    alias: "getHealth",
    description: `Проверка живости периметра: возвращает 200 со статусом ok. Не зависит от доступности нижележащих сервисов (это readiness, не liveness).
`,
    requestFormat: "json",
    response: HealthStatus,
  },
  {
    method: "get",
    path: "/iam/permissions",
    alias: "listPermissions",
    description: `Возвращает все права каталога (read-only). Требует право (read, iam:global); отказ/недоступность IDM → 403 (fail-closed).
`,
    requestFormat: "json",
    response: PermissionList,
    errors: [
      {
        status: 403,
        description: `Доступ запрещён (RBAC, fail-closed)`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
    ],
  },
  {
    method: "get",
    path: "/iam/roles",
    alias: "listRoles",
    description: `Возвращает все роли каталога (read-only). Привилегированная ручка IAM-админки: требует право (read, iam:global); отказ/недоступность IDM → 403 (fail-closed). Роли/права сидируются миграциями — UI их только показывает (ADR-0014).
`,
    requestFormat: "json",
    response: RoleList,
    errors: [
      {
        status: 403,
        description: `Доступ запрещён (RBAC, fail-closed)`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
    ],
  },
  {
    method: "get",
    path: "/iam/roles/:role/permissions",
    alias: "getRolePermissions",
    description: `Возвращает права роли (read-only). Требует право (read, iam:global); отказ/недоступность IDM → 403 (fail-closed); несуществующая роль → 404.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "role",
        type: "Path",
        schema: z.string().min(1),
      },
    ],
    response: PermissionList,
    errors: [
      {
        status: 403,
        description: `Доступ запрещён (RBAC, fail-closed)`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
      {
        status: 404,
        description: `Ресурс не найден`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
    ],
  },
  {
    method: "get",
    path: "/iam/subjects",
    alias: "listSubjects",
    description: `Возвращает страницу субъектов (DISTINCT subject из subject_roles) с их ролями; keyset-пагинация по subject. Субъекты без ролей системе неизвестны и не возвращаются. Требует право (read, iam:global); отказ/недоступность IDM → 403 (fail-closed).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "page_size",
        type: "Query",
        schema: z.number().int().gte(1).optional(),
      },
      {
        name: "page_token",
        type: "Query",
        schema: z.string().optional(),
      },
    ],
    response: SubjectList,
    errors: [
      {
        status: 400,
        description: `Некорректный запрос (валидация входных данных)`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
      {
        status: 403,
        description: `Доступ запрещён (RBAC, fail-closed)`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
    ],
  },
  {
    method: "get",
    path: "/iam/subjects/:subject/roles",
    alias: "getSubjectRoles",
    description: `Возвращает роли субъекта (пустой набор, не 404, если ролей нет). Требует право (read, iam:global); отказ/недоступность IDM → 403 (fail-closed).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "subject",
        type: "Path",
        schema: z.string().min(1),
      },
    ],
    response: SubjectRoles,
    errors: [
      {
        status: 403,
        description: `Доступ запрещён (RBAC, fail-closed)`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
    ],
  },
  {
    method: "post",
    path: "/iam/subjects/:subject/roles/:role",
    alias: "assignRole",
    description: `Назначает субъекту существующую роль (идемпотентно: повтор → 200). Требует право (write, iam:global); отказ/недоступность IDM → 403 (fail-closed). Несуществующая роль → 404; пустые subject/role → 400. После мутации IDM инвалидирует кэш решений по субъекту. Ответ — актуальный набор ролей субъекта (ADR-0014).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "subject",
        type: "Path",
        schema: z.string().min(1),
      },
      {
        name: "role",
        type: "Path",
        schema: z.string().min(1),
      },
    ],
    response: SubjectRoles,
    errors: [
      {
        status: 400,
        description: `Некорректный запрос (валидация входных данных)`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
      {
        status: 403,
        description: `Доступ запрещён (RBAC, fail-closed)`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
      {
        status: 404,
        description: `Ресурс не найден`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
    ],
  },
  {
    method: "delete",
    path: "/iam/subjects/:subject/roles/:role",
    alias: "revokeRole",
    description: `Снимает у субъекта роль (идемпотентно: снятие отсутствующей связки → 200). Требует право (write, iam:global); отказ/недоступность IDM → 403 (fail-closed); пустые subject/role → 400. После мутации IDM инвалидирует кэш решений по субъекту. Ответ — актуальный набор ролей субъекта.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "subject",
        type: "Path",
        schema: z.string().min(1),
      },
      {
        name: "role",
        type: "Path",
        schema: z.string().min(1),
      },
    ],
    response: SubjectRoles,
    errors: [
      {
        status: 400,
        description: `Некорректный запрос (валидация входных данных)`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
      {
        status: 403,
        description: `Доступ запрещён (RBAC, fail-closed)`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
    ],
  },
  {
    method: "get",
    path: "/projects/:project/services",
    alias: "listServices",
    description: `Возвращает страницу сервисов проекта с keyset-пагинацией (page_size/page_token). Требует право list на project:{project}; отказ RBAC → 403. Курсор непрозрачен и пробрасывается без интерпретации.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "project",
        type: "Path",
        schema: z.string().min(1),
      },
      {
        name: "page_size",
        type: "Query",
        schema: z.number().int().gte(1).optional(),
      },
      {
        name: "page_token",
        type: "Query",
        schema: z.string().optional(),
      },
    ],
    response: ServiceList,
    errors: [
      {
        status: 400,
        description: `Некорректный запрос (валидация входных данных)`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
    ],
  },
  {
    method: "post",
    path: "/projects/:project/services",
    alias: "createService",
    description: `Запускает создание сервиса в проекте (асинхронная провизия репозитория, образов и секретов). Запись каталога фиксируется со статусом creating; требует право create на project:{project} (отказ RBAC → 403). Занятое имя в проекте → 409.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: z.object({ name: z.string().min(1) }).passthrough(),
      },
      {
        name: "project",
        type: "Path",
        schema: z.string().min(1),
      },
    ],
    response: CreateServiceResult,
    errors: [
      {
        status: 400,
        description: `Некорректный запрос (валидация входных данных)`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
      {
        status: 409,
        description: `Конфликт состояния (например, имя сервиса уже занято)`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
    ],
  },
  {
    method: "get",
    path: "/projects/:project/services/:name",
    alias: "getService",
    description: `Возвращает текущее состояние сервиса (статус, владельцы, версия). Требует право read на project:{project} (отказ RBAC → 403); отсутствие сервиса → 404.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "project",
        type: "Path",
        schema: z.string().min(1),
      },
      {
        name: "name",
        type: "Path",
        schema: z.string().min(1),
      },
    ],
    response: ServiceSummary,
    errors: [
      {
        status: 404,
        description: `Ресурс не найден`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
    ],
  },
  {
    method: "post",
    path: "/projects/:project/services/:name/decommission",
    alias: "decommissionService",
    description: `Выводит активный сервис из эксплуатации (soft delete): данные каталога сохраняются, статус переводится в decommissioned, во внешних системах отзывается доступ (GitLab archive, Harbor read-only, Vault revoke). Тело несёт явное предусловие load_drained (нагрузка снята из K8s, ADR-0012). Идемпотентно: повтор на уже выведенном сервисе → 200. Допустим только исходный статус active; предусловие не выполнено (нагрузка не снята или статус creating/failed) → 422; конкурентная смена статуса → 409; отсутствие сервиса → 404; отказ RBAC (право decommission) → 403.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: z.object({ load_drained: z.boolean() }).passthrough(),
      },
      {
        name: "project",
        type: "Path",
        schema: z.string().min(1),
      },
      {
        name: "name",
        type: "Path",
        schema: z.string().min(1),
      },
    ],
    response: ServiceSummary,
    errors: [
      {
        status: 400,
        description: `Некорректный запрос (валидация входных данных)`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
      {
        status: 403,
        description: `Доступ запрещён (RBAC, fail-closed)`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
      {
        status: 404,
        description: `Ресурс не найден`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
      {
        status: 409,
        description: `Конфликт состояния (например, имя сервиса уже занято)`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
      {
        status: 422,
        description: `Предусловие операции не выполнено (например, нагрузка не снята из K8s или недопустимый исходный статус сервиса).
`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
    ],
  },
  {
    method: "put",
    path: "/projects/:project/services/:name/owners",
    alias: "setServiceOwners",
    description: `Заменяет набор владельцев целиком (идемпотентно). Клиент передаёт полный желаемый набор owners и текущую версию owners_version (optimistic-concurrency). Несовпадение версии → 409; отсутствие сервиса → 404; отказ RBAC (право change_owners) → 403.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: SetServiceOwnersRequest,
      },
      {
        name: "project",
        type: "Path",
        schema: z.string().min(1),
      },
      {
        name: "name",
        type: "Path",
        schema: z.string().min(1),
      },
    ],
    response: SetServiceOwnersResult,
    errors: [
      {
        status: 400,
        description: `Некорректный запрос (валидация входных данных)`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
      {
        status: 403,
        description: `Доступ запрещён (RBAC, fail-closed)`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
      {
        status: 404,
        description: `Ресурс не найден`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
      {
        status: 409,
        description: `Конфликт состояния (например, имя сервиса уже занято)`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
    ],
  },
  {
    method: "post",
    path: "/projects/:project/services/:name/transfer",
    alias: "transferService",
    description: `Переносит сервис в другой проект (смена project-владельца): id записи каталога сохраняется, меняется проект (source→target), владельцы переезжают вместе с записью. Перенос выполняется асинхронно (Saga) с ТОЧКОЙ НЕВОЗВРАТА на переносе репозитория GitLab: каталог active→transferring → transfer GitLab → миграция путей Vault → метаданные Harbor → каталог transferring→active (project&#x3D;target) → перенос ролей владельцев в IDM. Требует ДВУХ прав: transfer на исходном проекте И transfer_in на целевом (ADR-0013). Идемпотентно: повтор на уже перенесённом сервисе → 200. Допустим только исходный статус active; недопустимый статус → 422; занятое имя в target или конкурентная смена → 409; отсутствие сервиса → 404; отказ RBAC (transfer/transfer_in) → 403.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: z.object({ target_project: z.string().min(1) }).passthrough(),
      },
      {
        name: "project",
        type: "Path",
        schema: z.string().min(1),
      },
      {
        name: "name",
        type: "Path",
        schema: z.string().min(1),
      },
    ],
    response: ServiceSummary,
    errors: [
      {
        status: 400,
        description: `Некорректный запрос (валидация входных данных)`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
      {
        status: 403,
        description: `Доступ запрещён (RBAC, fail-closed)`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
      {
        status: 404,
        description: `Ресурс не найден`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
      {
        status: 409,
        description: `Конфликт состояния (например, имя сервиса уже занято)`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
      {
        status: 422,
        description: `Предусловие операции не выполнено (например, нагрузка не снята из K8s или недопустимый исходный статус сервиса).
`,
        schema: z.object({ error: z.string() }).passthrough(),
      },
    ],
  },
]);

export const api = new Zodios(endpoints);

export function createApiClient(baseUrl: string, options?: ZodiosOptions) {
  return new Zodios(baseUrl, endpoints, options);
}
