// Экран формы создания сервиса. Источник правды клиентской валидации — zod-схема
// CreateServiceRequest из кодогена (имя и обязательный набор владельцев). Поле
// владельцев вводится свободным текстом (по строкам/запятым) и разбирается тем же
// parseOwners, что и карточка владельцев; набор обязателен (минимум один). По
// успеху переходим на экран прогресса создаваемого сервиса; конфликт имени (409)
// показываем как понятную ошибку без внутренних деталей сервера.
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { useMutation } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";
import { ArrowLeft, Loader2 } from "lucide-react";
import { z } from "zod";

import { apiClient, schemas } from "@/api";
import { parseOwners } from "@/components/OwnersCard";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { useToast } from "@/components/ui/toast";

// ownersSchema — ограничение владельцев из кодогена (массив непустых строк, минимум
// один). Используем его как источник правды при валидации разобранного набора.
const ownersSchema = schemas.CreateServiceRequest.shape.owners;

// createFormSchema — форма создания: имя берёт ограничение из кодогена, владельцы
// вводятся текстом и валидируются через ownersSchema после разбора parseOwners.
const createFormSchema = z
  .object({
    name: schemas.CreateServiceRequest.shape.name,
    ownersText: z.string(),
  })
  .superRefine((values, ctx) => {
    if (!ownersSchema.safeParse(parseOwners(values.ownersText)).success) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ["ownersText"],
        message: "Укажите хотя бы одного владельца",
      });
    }
  });

// FormValues — тип данных формы (имя + свободный текст владельцев).
type FormValues = z.infer<typeof createFormSchema>;

export function CreateServicePage() {
  const { project = "" } = useParams();
  const navigate = useNavigate();
  const toast = useToast();

  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<FormValues>({
    resolver: zodResolver(createFormSchema),
    defaultValues: { name: "", ownersText: "" },
  });

  const mutation = useMutation({
    mutationFn: (values: FormValues) =>
      apiClient.createService(
        { name: values.name, owners: parseOwners(values.ownersText) },
        { params: { project } },
      ),
    onSuccess: (_data, values) => {
      navigate(`/projects/${project}/services/${values.name}`);
    },
    // Ошибки — через единый тост (маппинг кодов периметра); конфликт имени (409)
    // показываем понятным сообщением без раскрытия внутренних деталей.
    onError: (err) =>
      toast.error(err, {
        action: "запустить создание сервиса",
        overrides: { 409: "Сервис с таким именем уже существует в проекте." },
      }),
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
            создаются автоматически, а указанные владельцы получают доступ.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form
            className="flex flex-col gap-4"
            onSubmit={handleSubmit((values) => mutation.mutate(values))}
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

            <div className="flex flex-col gap-1.5">
              <label htmlFor="ownersText" className="text-sm font-medium">
                Владельцы
              </label>
              <textarea
                id="ownersText"
                rows={4}
                className="rounded-md border border-input bg-background px-3 py-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring aria-[invalid=true]:border-destructive"
                placeholder="alice&#10;bob"
                aria-invalid={Boolean(errors.ownersText)}
                {...register("ownersText")}
              />
              <p className="text-xs text-muted-foreground">
                По одному в строке или через запятую. Поле обязательно.
              </p>
              {errors.ownersText && (
                <p className="text-sm text-destructive">
                  {errors.ownersText.message ?? "Укажите владельцев"}
                </p>
              )}
            </div>

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
