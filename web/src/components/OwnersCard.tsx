// Карточка владельцев сервиса: отображение текущего состава и форма его
// изменения. Источник правды клиентской валидации — zod (через zodResolver);
// мутация идёт через периметр (PUT /projects/{project}/services/{name}/owners) с
// optimistic-concurrency по owners_version. Обрабатываем 403 (нет права) и 409
// (конфликт версии — устаревшие данные) понятными сообщениями без раскрытия
// внутренних деталей сервера.
import { useEffect, useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, Loader2, Users } from "lucide-react";
import { z } from "zod";

import { apiClient } from "@/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";

// ownersFormSchema — форма ввода владельцев: свободный текст (по строкам/запятым).
// Преобразование в нормализованный набор делает parseOwners.
const ownersFormSchema = z.object({ ownersText: z.string() });
type OwnersForm = z.infer<typeof ownersFormSchema>;

// parseOwners разбивает текст на владельцев: разделители — перевод строки и
// запятая; пустые отбрасываются, дубли убираются (детерминированный порядок).
export function parseOwners(text: string): string[] {
  const seen = new Set<string>();
  for (const raw of text.split(/[\n,]/)) {
    const owner = raw.trim();
    if (owner) seen.add(owner);
  }
  return [...seen].sort();
}

// httpStatusOf аккуратно достаёт HTTP-статус из ошибки zodios/axios.
function httpStatusOf(err: unknown): number | undefined {
  if (typeof err === "object" && err !== null && "response" in err) {
    return (err as { response?: { status?: number } }).response?.status;
  }
  return undefined;
}

type Props = {
  project: string;
  name: string;
  owners: string[];
  ownersVersion: number;
};

export function OwnersCard({ project, name, owners, ownersVersion }: Props) {
  const queryClient = useQueryClient();
  const [serverError, setServerError] = useState<string | null>(null);

  const { register, handleSubmit, reset } = useForm<OwnersForm>({
    resolver: zodResolver(ownersFormSchema),
    defaultValues: { ownersText: owners.join("\n") },
  });

  // Синхронизируем поле формы при обновлении владельцев из ответа сервера.
  useEffect(() => {
    reset({ ownersText: owners.join("\n") });
  }, [owners, reset]);

  const mutation = useMutation({
    mutationFn: (values: OwnersForm) =>
      apiClient.setServiceOwners(
        { owners: parseOwners(values.ownersText), owners_version: ownersVersion },
        { params: { project, name } },
      ),
    onSuccess: () => {
      setServerError(null);
      // Перечитываем сервис: актуальные владельцы и версия.
      void queryClient.invalidateQueries({ queryKey: ["service", project, name] });
    },
    onError: (err) => {
      const code = httpStatusOf(err);
      if (code === 403) {
        setServerError("Недостаточно прав для изменения владельцев.");
      } else if (code === 409) {
        setServerError(
          "Состав владельцев изменился в другом месте. Обновите страницу и повторите.",
        );
      } else {
        setServerError("Не удалось изменить владельцев. Повторите попытку.");
      }
    },
  });

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <Users className="size-4" />
          Владельцы
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {owners.length > 0 ? (
          <ul className="flex flex-wrap gap-2" aria-label="Текущие владельцы">
            {owners.map((o) => (
              <li
                key={o}
                className="rounded-md bg-muted px-2.5 py-1 text-sm text-foreground"
              >
                {o}
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-sm text-muted-foreground">Владельцы не назначены.</p>
        )}

        <form
          className="flex flex-col gap-3"
          onSubmit={handleSubmit((values) => {
            setServerError(null);
            mutation.mutate(values);
          })}
        >
          <label htmlFor="ownersText" className="text-sm font-medium">
            Новый состав владельцев (по одному в строке или через запятую)
          </label>
          <textarea
            id="ownersText"
            rows={4}
            className="rounded-md border border-input bg-background px-3 py-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
            placeholder="alice&#10;bob"
            {...register("ownersText")}
          />

          {serverError && (
            <p className="flex items-center gap-2 rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">
              <AlertTriangle className="size-4 shrink-0" />
              {serverError}
            </p>
          )}

          <div className="flex justify-end">
            <Button type="submit" disabled={mutation.isPending}>
              {mutation.isPending && <Loader2 className="size-4 animate-spin" />}
              {mutation.isPending ? "Сохраняем…" : "Сохранить владельцев"}
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}
