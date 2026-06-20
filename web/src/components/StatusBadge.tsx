// Бейдж статуса сервиса каталога (ADR-0004): цвет, иконка и подпись по статусу.
import { CheckCircle2, Loader2, XCircle, Archive, HelpCircle, ArrowLeftRight } from "lucide-react";

import { Badge } from "@/components/ui/badge";

type ServiceStatus = "creating" | "active" | "decommissioned" | "failed" | "transferring" | string;

// мета описывает оформление бейджа для каждого статуса.
const meta: Record<
  string,
  { label: string; variant: "default" | "secondary" | "success" | "warning" | "destructive"; spin?: boolean; Icon: typeof CheckCircle2 }
> = {
  creating: { label: "Создаётся", variant: "warning", spin: true, Icon: Loader2 },
  active: { label: "Активен", variant: "success", Icon: CheckCircle2 },
  failed: { label: "Ошибка", variant: "destructive", Icon: XCircle },
  decommissioned: { label: "Выведен", variant: "secondary", Icon: Archive },
  transferring: { label: "Переносится", variant: "warning", spin: true, Icon: ArrowLeftRight },
};

export function StatusBadge({ status }: { status: ServiceStatus }) {
  const m = meta[status] ?? {
    label: status || "неизвестно",
    variant: "secondary" as const,
    Icon: HelpCircle,
  };
  return (
    <Badge variant={m.variant}>
      <m.Icon className={m.spin ? "animate-spin" : undefined} />
      {m.label}
    </Badge>
  );
}
