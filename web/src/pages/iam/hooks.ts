// Общие IAM-запросы, мутации и маппинги кодов периметра (ADR-0014/0015/0016) для
// трёх страниц раздела «Роли и доступы» (Роли/Права/Пользователи). Хуки используют
// общие queryKey, поэтому кэш react-query разделяется между страницами (listRoles/
// listPermissions не дублируются по сети). Мутации показывают результат тостом с
// единым маппингом кодов и инвалидируют соответствующие запросы; UI-побочные эффекты
// (закрыть модалку, перейти по маршруту) страница добавляет через onSuccess в mutate().
import { useEffect, useState } from "react";
import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";

import { apiClient } from "@/api";
import { useToast } from "@/components/ui/toast";

// Единый маппинг кодов для действий назначения/снятия роли (write на iam:global).
export const ASSIGN_OVERRIDES: Record<number, string> = {
  403: "Недостаточно прав для управления ролями (нужно право write на iam:global).",
  404: "Роль не найдена.",
  400: "Некорректные данные запроса.",
};

// Единый маппинг кодов для структурных мутаций каталога (manage на iam:global).
export const CATALOG_OVERRIDES: Record<number, string> = {
  403: "Недостаточно прав для изменения каталога (нужно право manage на iam:global).",
  404: "Роль или право не найдены.",
  409: "Такая роль или право уже существуют.",
  422: "Системные роли и права защищены от изменения.",
  400: "Некорректные данные запроса.",
};

// useDebounced возвращает значение с задержкой: пикер пользователя не дёргает
// справочник на каждый набранный символ (ADR-0016).
export function useDebounced<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState<T>(value);
  useEffect(() => {
    const id = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(id);
  }, [value, delayMs]);
  return debounced;
}

// ── Запросы (чтение) ───────────────────────────────────────────────────────────

// useRolesQuery — каталог ролей. 403 здесь = нет права на админку (fail-closed).
export function useRolesQuery() {
  return useQuery({
    queryKey: ["iam", "roles"],
    queryFn: () => apiClient.listRoles(),
    retry: false,
  });
}

// usePermissionsQuery — каталог прав (нужен и для attach к роли).
export function usePermissionsQuery() {
  return useQuery({
    queryKey: ["iam", "permissions"],
    queryFn: () => apiClient.listPermissions(),
    retry: false,
  });
}

// useRolePermissionsQuery — права выбранной роли (включается при выбранной роли).
export function useRolePermissionsQuery(role: string | null) {
  return useQuery({
    queryKey: ["iam", "role-permissions", role],
    queryFn: () => apiClient.getRolePermissions({ params: { role: role ?? "" } }),
    enabled: role !== null,
    retry: false,
  });
}

// useSubjectsInfiniteQuery — список субъектов с ролями (курсорная пагинация, ADR-0009).
export function useSubjectsInfiniteQuery() {
  return useInfiniteQuery({
    queryKey: ["iam", "subjects"],
    initialPageParam: "",
    queryFn: ({ pageParam }) =>
      apiClient.listSubjects({ queries: { page_token: pageParam || undefined } }),
    getNextPageParam: (lastPage) => lastPage.next_page_token || undefined,
    retry: false,
  });
}

// useDirectorySearch — поиск в справочнике Keycloak (read iam:directory). Запускается
// от 2 символов; 403/503 обрабатываются на странице.
export function useDirectorySearch(debouncedSearch: string) {
  return useQuery({
    queryKey: ["iam", "directory", debouncedSearch],
    queryFn: () =>
      apiClient.searchDirectorySubjects({ queries: { search: debouncedSearch } }),
    enabled: debouncedSearch.trim().length >= 2,
    retry: false,
  });
}

// ── Мутации (запись / структурное управление) ───────────────────────────────────

// useInvalidate — хелперы инвалидации списков после мутаций.
function useInvalidate() {
  const queryClient = useQueryClient();
  return {
    subjects: () => queryClient.invalidateQueries({ queryKey: ["iam", "subjects"] }),
    roles: () => queryClient.invalidateQueries({ queryKey: ["iam", "roles"] }),
    permissions: () => queryClient.invalidateQueries({ queryKey: ["iam", "permissions"] }),
    rolePerms: () => queryClient.invalidateQueries({ queryKey: ["iam", "role-permissions"] }),
  };
}

export function useAssignRole() {
  const toast = useToast();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: (vars: { subject: string; role: string }) =>
      apiClient.assignRole(undefined, { params: { subject: vars.subject, role: vars.role } }),
    onSuccess: () => {
      toast.success("Роль назначена.");
      void invalidate.subjects();
    },
    onError: (err) => toast.error(err, { action: "назначить роль", overrides: ASSIGN_OVERRIDES }),
  });
}

export function useRevokeRole() {
  const toast = useToast();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: (vars: { subject: string; role: string }) =>
      apiClient.revokeRole(undefined, { params: { subject: vars.subject, role: vars.role } }),
    onSuccess: () => {
      toast.success("Роль снята.");
      void invalidate.subjects();
    },
    onError: (err) => toast.error(err, { action: "снять роль", overrides: ASSIGN_OVERRIDES }),
  });
}

export function useCreateRole() {
  const toast = useToast();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: (vars: { name: string }) => apiClient.createRole({ name: vars.name }),
    onSuccess: () => {
      toast.success("Роль создана.");
      void invalidate.roles();
    },
    onError: (err) => toast.error(err, { action: "создать роль", overrides: CATALOG_OVERRIDES }),
  });
}

export function useDeleteRole() {
  const toast = useToast();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: (role: string) => apiClient.deleteRole(undefined, { params: { role } }),
    onSuccess: () => {
      toast.success("Роль удалена.");
      void invalidate.roles();
      void invalidate.subjects();
    },
    onError: (err) => toast.error(err, { action: "удалить роль", overrides: CATALOG_OVERRIDES }),
  });
}

export function useCreatePermission() {
  const toast = useToast();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: (vars: { action: string; resource: string }) =>
      apiClient.createPermission({ action: vars.action, resource: vars.resource }),
    onSuccess: () => {
      toast.success("Право создано.");
      void invalidate.permissions();
    },
    onError: (err) => toast.error(err, { action: "создать право", overrides: CATALOG_OVERRIDES }),
  });
}

export function useDeletePermission() {
  const toast = useToast();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: (vars: { action: string; resource: string }) =>
      apiClient.deletePermission(undefined, { queries: vars }),
    onSuccess: () => {
      toast.success("Право удалено.");
      void invalidate.permissions();
      void invalidate.rolePerms();
    },
    onError: (err) => toast.error(err, { action: "удалить право", overrides: CATALOG_OVERRIDES }),
  });
}

export function useAttachPermission() {
  const toast = useToast();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: (vars: { role: string; action: string; resource: string }) =>
      apiClient.attachPermission(
        { action: vars.action, resource: vars.resource },
        { params: { role: vars.role } },
      ),
    onSuccess: () => {
      toast.success("Право прикреплено.");
      void invalidate.rolePerms();
    },
    onError: (err) => toast.error(err, { action: "прикрепить право", overrides: CATALOG_OVERRIDES }),
  });
}

export function useDetachPermission() {
  const toast = useToast();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: (vars: { role: string; action: string; resource: string }) =>
      apiClient.detachPermission(undefined, {
        params: { role: vars.role },
        queries: { action: vars.action, resource: vars.resource },
      }),
    onSuccess: () => {
      toast.success("Право откреплено.");
      void invalidate.rolePerms();
    },
    onError: (err) => toast.error(err, { action: "открепить право", overrides: CATALOG_OVERRIDES }),
  });
}
