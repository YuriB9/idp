// Глобальный каркас портала: боковая навигация + шапка. Единый визуальный язык
// с остальными фронтендами платформы (zinc/Geist, sidebar + header).
import { Boxes, FileCode2, Layers, ServerCog, ShieldCheck } from "lucide-react";
import { NavLink, Outlet, useParams } from "react-router-dom";

import { ThemeToggle } from "@/components/ThemeToggle";
import { cn } from "@/lib/utils";

// DEFAULT_PROJECT — проект по умолчанию для MVP-демо (выбор проекта — позже).
export const DEFAULT_PROJECT = "demo";

const navLinkClass = ({ isActive }: { isActive: boolean }) =>
  cn(
    "flex items-center gap-2.5 rounded-md px-3 py-2 text-sm font-medium transition-colors",
    isActive
      ? "bg-accent text-accent-foreground"
      : "text-muted-foreground hover:bg-accent/50 hover:text-foreground",
  );

export function GlobalLayout() {
  const { project = DEFAULT_PROJECT } = useParams();

  return (
    <div className="flex h-screen overflow-hidden bg-background text-foreground">
      {/* Боковая навигация */}
      <aside className="flex w-56 flex-shrink-0 flex-col border-r border-border">
        <div className="flex h-14 items-center gap-2 border-b border-border px-4">
          <Boxes className="size-5 text-primary" />
          <span className="text-sm font-semibold">IDP Platform</span>
        </div>
        <nav className="flex flex-col gap-1 p-2">
          <NavLink to={`/projects/${project}/services`} className={navLinkClass} end>
            <Layers className="size-4" />
            Сервисы
          </NavLink>
          <NavLink to="/iam" className={navLinkClass}>
            <ShieldCheck className="size-4" />
            Роли и доступы
          </NavLink>
          {/* Статическая страница Swagger UI (вне react-router) — обычная ссылка
              с полной навигацией; открываем в новой вкладке. Одиночный файл
              /swagger.html, чтобы не зависеть от директорийного индекса. */}
          <a
            href="/swagger.html"
            target="_blank"
            rel="noreferrer"
            className={navLinkClass({ isActive: false })}
          >
            <FileCode2 className="size-4" />
            API (Swagger)
          </a>
        </nav>
        <div className="mt-auto p-3 text-xs text-muted-foreground">
          Проект:{" "}
          <span className="font-medium text-foreground">{project}</span>
        </div>
      </aside>

      {/* Основная область */}
      <div className="flex flex-1 flex-col overflow-hidden">
        <header className="flex h-14 flex-shrink-0 items-center gap-2 border-b border-border px-6">
          <ServerCog className="size-4 text-muted-foreground" />
          <span className="text-sm text-muted-foreground">
            Каталог сервисов команды DevInfra
          </span>
          {/* Переключатель светлой/тёмной темы — у правого края шапки. */}
          <div className="ml-auto">
            <ThemeToggle />
          </div>
        </header>
        <main className="flex-1 overflow-y-auto p-6">
          <div className="mx-auto max-w-3xl">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  );
}
