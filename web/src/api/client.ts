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

export const schemas = {
  HealthStatus,
  ServiceStatus,
  ServiceSummary,
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
    path: "/services",
    alias: "listServices",
    requestFormat: "json",
    response: z.array(ServiceSummary),
  },
]);

export const api = new Zodios(endpoints);

export function createApiClient(baseUrl: string, options?: ZodiosOptions) {
  return new Zodios(baseUrl, endpoints, options);
}
