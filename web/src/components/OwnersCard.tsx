// Карточка владельцев сервиса: отображение текущего состава и изменение через
// модальное окно (Dialog, ADR-0017). Источник правды клиентской валидации формы —
// zod (через zodResolver); мутация идёт через периметр (PUT .../owners) с
// optimistic-concurrency по owners_version. Результат — через тосты (единый маппинг
// 403/409), без раскрытия внутренних деталей сервера.
import { useEffect, useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Loader2, Users } from "lucide-react";
import { z } from "zod";

import { apiClient } from "@/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { useToast } from "@/components/ui/toast";

// ownersFormSchema — форма ввода владельцев: свободный текст (по строкам/запятым).
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

type Props = {
  project: string;
  name: string;
  owners: string[];
  ownersVersion: number;
  // onStarted — вызывается после принятого запуска смены владельцев; страница
  // поднимает единый ступенчатый прогресс (вместо тоста об успехе).
  onStarted?: () => void;
};

export function OwnersCard({ project, name, owners, ownersVersion, onStarted }: Props) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const [open, setOpen] = useState(false);

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
      // Успех показываем не тостом, а единым ступенчатым прогрессом на странице.
      setOpen(false);
      onStarted?.();
      void queryClient.invalidateQueries({ queryKey: ["service", project, name] });
    },
    onError: (err) =>
      toast.error(err, {
        action: "изменить владельцев",
        overrides: {
          403: "Недостаточно прав для изменения владельцев.",
          409: "Состав владельцев изменился в другом месте. Обновите страницу и повторите.",
        },
      }),
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

        <div className="flex justify-end">
          <Button type="button" variant="outline" onClick={() => setOpen(true)}>
            Изменить владельцев
          </Button>
        </div>

        <Dialog
          open={open}
          onClose={() => setOpen(false)}
          title="Изменить владельцев"
          description="Укажите полный желаемый состав владельцев (по одному в строке или через запятую)."
          footer={
            <>
              <Button
                type="button"
                variant="ghost"
                onClick={() => setOpen(false)}
                disabled={mutation.isPending}
              >
                Отмена
              </Button>
              <Button type="submit" form="owners-form" disabled={mutation.isPending}>
                {mutation.isPending && <Loader2 className="size-4 animate-spin" />}
                Сохранить владельцев
              </Button>
            </>
          }
        >
          <form
            id="owners-form"
            className="flex flex-col gap-2"
            onSubmit={handleSubmit((values) => mutation.mutate(values))}
          >
            <label htmlFor="ownersText" className="text-sm font-medium">
              Новый состав владельцев
            </label>
            <textarea
              id="ownersText"
              rows={4}
              className="rounded-md border border-input bg-background px-3 py-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
              placeholder="alice&#10;bob"
              {...register("ownersText")}
            />
          </form>
        </Dialog>
      </CardContent>
    </Card>
  );
}
