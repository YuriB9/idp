// Экран формы создания сервиса. Источник правды клиентской валидации — zod-схема
// CreateServiceRequest из кодогена (через zodResolver). По успеху переходим на
// экран прогресса создаваемого сервиса; конфликт имени (409) показываем как
// понятную ошибку без внутренних деталей сервера.
import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { useMutation } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";
import { AlertTriangle, ArrowLeft, Loader2 } from "lucide-react";
import type { z } from "zod";

import { apiClient, schemas } from "@/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";

// FormValues — тип данных формы, выведенный из zod-схемы периметра.
type FormValues = z.infer<typeof schemas.CreateServiceRequest>;

// httpStatusOf аккуратно достаёт HTTP-статус из ошибки zodios/axios.
function httpStatusOf(err: unknown): number | undefined {
  if (typeof err === "object" && err !== null && "response" in err) {
    const resp = (err as { response?: { status?: number } }).response;
    return resp?.status;
  }
  return undefined;
}

export function CreateServicePage() {
  const { project = "" } = useParams();
  const navigate = useNavigate();
  const [serverError, setServerError] = useState<string | null>(null);

  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<FormValues>({
    resolver: zodResolver(schemas.CreateServiceRequest),
    defaultValues: { name: "" },
  });

  const mutation = useMutation({
    mutationFn: (values: FormValues) =>
      apiClient.createService(values, { params: { project } }),
    onSuccess: (_data, values) => {
      navigate(`/projects/${project}/services/${values.name}`);
    },
    onError: (err) => {
      setServerError(
        httpStatusOf(err) === 409
          ? "Сервис с таким именем уже существует в проекте."
          : "Не удалось запустить создание сервиса. Повторите попытку.",
      );
    },
  });

  return (
    <section className="flex flex-col gap-5">
      <Link
        to={`/projects/${project}/services`}
        className="inline-flex w-fit items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
      >
        <ArrowLeft className="size-4" />
        К списку сервисов
      </Link>

      <Card>
        <CardHeader>
          <CardTitle>Создание сервиса</CardTitle>
          <CardDescription>
            Запуск провизии в проекте «{project}»: репозиторий, образы и секреты
            создаются автоматически.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form
            className="flex flex-col gap-4"
            onSubmit={handleSubmit((values) => {
              setServerError(null);
              mutation.mutate(values);
            })}
          >
            <div className="flex flex-col gap-1.5">
              <label htmlFor="name" className="text-sm font-medium">
                Имя сервиса
              </label>
              <Input
                id="name"
                placeholder="например, billing-api"
                aria-invalid={Boolean(errors.name)}
                {...register("name")}
              />
              {errors.name && (
                <p className="text-sm text-destructive">
                  {errors.name.message ?? "Некорректное имя"}
                </p>
              )}
            </div>

            {serverError && (
              <p className="flex items-center gap-2 rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">
                <AlertTriangle className="size-4 shrink-0" />
                {serverError}
              </p>
            )}

            <div className="flex justify-end">
              <Button type="submit" disabled={mutation.isPending}>
                {mutation.isPending && <Loader2 className="size-4 animate-spin" />}
                {mutation.isPending ? "Создаём…" : "Создать"}
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>
    </section>
  );
}
