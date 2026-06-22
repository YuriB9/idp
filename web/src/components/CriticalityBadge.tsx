// Бейдж уровня критичности системы (дизайн-система, ADR-0017). Единый источник
// правды визуальной иерархии критичности: каждый уровень кодируется ОДНОВРЕМЕННО
// цветом, иконкой и текстом (colorblind-safe, не только цветом). Неизвестный
// уровень деградирует в нейтральный бейдж с исходной строкой.
import { ArrowDown, Minus, ArrowUp, Flame, HelpCircle } from "lucide-react";

import { Badge } from "@/components/ui/badge";

// Criticality — поддерживаемые уровни критичности (строка допускает деградацию).
export type Criticality = "low" | "medium" | "high" | "critical" | (string & {});

// criticalityMeta — ЕДИНАЯ таблица оформления уровней: подпись, классы цвета
// (подложка цвета/15 + сплошной текст токена) и иконка. Классы записаны литерально,
// чтобы Tailwind JIT их сгенерировал.
const criticalityMeta: Record<
  string,
  { label: string; className: string; Icon: typeof Minus }
> = {
  low: {
    label: "Низкая",
    className: "bg-criticality-low/15 text-criticality-low",
    Icon: ArrowDown,
  },
  medium: {
    label: "Средняя",
    className: "bg-criticality-medium/15 text-criticality-medium",
    Icon: Minus,
  },
  high: {
    label: "Высокая",
    className: "bg-criticality-high/15 text-criticality-high",
    Icon: ArrowUp,
  },
  critical: {
    label: "Критическая",
    className: "bg-criticality-critical/15 text-criticality-critical",
    Icon: Flame,
  },
};

export function CriticalityBadge({ level }: { level: Criticality }) {
  const meta = criticalityMeta[level] ?? {
    label: level || "неизвестно",
    className: "bg-muted text-muted-foreground",
    Icon: HelpCircle,
  };
  return (
    <Badge variant="outline" className={meta.className}>
      <meta.Icon />
      {meta.label}
    </Badge>
  );
}
