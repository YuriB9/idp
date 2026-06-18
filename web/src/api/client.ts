import { makeApi, Zodios, type ZodiosOptions } from "@zodios/core";
import { z } from "zod";

const HealthStatus = z.object({ status: z.literal("ok") }).passthrough();
const ServiceStatus = z.enum([
  "creating",
  "active",
  "decommissioned",
  "failed",
]);
const ServiceSummary = z
  .object({ project: z.string(), name: z.string(), status: ServiceStatus })
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

export const schemas = {
  HealthStatus,
  ServiceStatus,
  ServiceSummary,
  ServiceList,
  Error,
  CreateServiceRequest,
  CreateServiceResult,
};

const endpoints = makeApi([
  {
    method: "get",
    path: "/healthz",
    alias: "getHealth",
    requestFormat: "json",
    response: HealthStatus,
  },
  {
    method: "get",
    path: "/projects/:project/services",
    alias: "listServices",
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
]);

export const api = new Zodios(endpoints);

export function createApiClient(baseUrl: string, options?: ZodiosOptions) {
  return new Zodios(baseUrl, endpoints, options);
}
