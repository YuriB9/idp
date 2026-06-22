// Глобальный каркас портала: раздвигающееся боковое меню (Sidebar) + шапка с
// мобильным переключателем меню и переключателем темы. Единый визуальный язык
// дизайн-системы (ADR-0017). Маршруты и разделы сохранены; на узких экранах меню
// открывается off-canvas.
import { useState } from "react";
import { Boxes, FileCode2, Layers, Menu, ServerCog, ShieldCheck } from "lucide-react";
import { Outlet, useParams } from "react-router-dom";

import { ThemeToggle } from "@/components/ThemeToggle";
import { Sidebar, type SidebarGroup } from "@/components/Sidebar";

// DEFAULT_PROJECT — проект по умолчанию для MVP-демо (выбор проекта — позже).
export const DEFAULT_PROJECT = "demo";

export function GlobalLayout() {
  const { project = DEFAULT_PROJECT } = useParams();
  const [mobileOpen, setMobileOpen] = useState(false);

  // Группы навигации портала. Ссылка на Swagger — внешняя (вне react-router).
  const groups: SidebarGroup[] = [
    {
      label: "Платформа",
      items: [
        { label: "Сервисы", to: `/projects/${project}/services`, icon: Layers, end: true },
        { label: "Роли и доступы", to: "/iam", icon: ShieldCheck },
        { label: "API (Swagger)", to: "/swagger.html", icon: FileCode2, external: true },
      ],
    },
  ];

  return (
    <div className="flex h-screen overflow-hidden bg-background text-foreground">
      <Sidebar
        brand="IDP Platform"
        brandIcon={Boxes}
        groups={groups}
        mobileOpen={mobileOpen}
        onMobileClose={() => setMobileOpen(false)}
        footer={
          <span>
            Проект: <span className="font-medium text-foreground">{project}</span>
          </span>
        }
      />

      <div className="flex flex-1 flex-col overflow-hidden">
        <header className="flex h-14 flex-shrink-0 items-center gap-2 border-b border-border px-4 md:px-6">
          {/* Мобильный переключатель off-canvas меню (виден только на узких экранах). */}
          <button
            type="button"
            aria-label="Открыть меню"
            onClick={() => setMobileOpen(true)}
            className="rounded-md p-1.5 text-muted-foreground outline-none hover:bg-accent/50 hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring md:hidden"
          >
            <Menu className="size-5" />
          </button>
          <ServerCog className="size-4 text-muted-foreground" />
          <span className="text-sm text-muted-foreground">
            Каталог сервисов команды DevInfra
          </span>
          {/* Переключатель светлой/тёмной темы — у правого края шапки. */}
          <div className="ml-auto">
            <ThemeToggle />
          </div>
        </header>
        <main className="flex-1 overflow-y-auto p-4 md:p-6">
          <div className="mx-auto w-full max-w-6xl">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  );
}
