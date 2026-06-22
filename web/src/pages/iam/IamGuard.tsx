// Единый guard fail-closed для страниц раздела «Роли и доступы» (ADR-0014). При 403 на
// ключевой запрос страницы (нет права read на iam:global) содержимое каталога не
// показывается — рендерится явный отказ доступа. При иных ошибках/успехе рендерятся
// дети (каждая страница сама обрабатывает свои состояния загрузки/ошибки). Так
// согласуется fail-closed периметра и UI (сырые внутренние ошибки наружу не идут).
import type { ReactNode } from "react";
import { ShieldX } from "lucide-react";

import { Card } from "@/components/ui/card";
import { httpStatusOf } from "@/lib/errors";

export type IamGuardProps = {
  // error — ошибка ключевого запроса страницы (например, каталога ролей/прав/субъектов).
  error: unknown;
  children: ReactNode;
};

// isIamForbidden — true, если ошибка ключевого запроса означает 403 (нет права на админку).
export function isIamForbidden(error: unknown): boolean {
  return httpStatusOf(error) === 403;
}

export function IamGuard({ error, children }: IamGuardProps) {
  if (isIamForbidden(error)) {
    return (
      <Card className="flex flex-col items-center gap-2 p-10 text-center">
        <ShieldX className="size-8 text-muted-foreground" />
        <p className="text-sm font-medium">Доступ к разделу запрещён</p>
        <p className="text-sm text-muted-foreground">
          У вас нет права на управление ролями и доступами (IAM-админка).
        </p>
      </Card>
    );
  }
  return <>{children}</>;
}
