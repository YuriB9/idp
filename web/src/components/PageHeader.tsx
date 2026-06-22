// Единый заголовок страницы (PageHeader, ADR-0017): консистентная подача
// «заголовок + подзаголовок + действия» во всех разделах портала. Заголовок —
// семантический <h1>; действия выровнены вправо.
import { type ReactNode } from "react";

export type PageHeaderProps = {
  title: ReactNode;
  description?: ReactNode;
  // actions — кнопки/ссылки действий уровня страницы (выровнены вправо).
  actions?: ReactNode;
};

export function PageHeader({ title, description, actions }: PageHeaderProps) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-3">
      <div className="flex flex-col gap-1">
        <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
        {description && <p className="text-sm text-muted-foreground">{description}</p>}
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
  );
}
