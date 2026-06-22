// Раздвигающееся левое меню портала (collapsible sidebar, ADR-0017). Группировка
// разделов, подсветка активного маршрута (react-router → aria-current), свёрнутый
// icon-only режим с тултипами, сохранение состояния в localStorage, клавиатурная
// навигация и ARIA-роли. На узких экранах — off-canvas режим (выезжающая панель с
// затемнением). Недоступность localStorage не ломает меню (деградация к дефолту).
import { useEffect, useState, type ComponentType } from "react";
import { NavLink } from "react-router-dom";
import { PanelLeftClose, PanelLeftOpen, X } from "lucide-react";

import { cn } from "@/lib/utils";
import {
  readSidebarState,
  writeSidebarState,
  type SidebarState,
} from "@/lib/ui-state";

// SidebarItem — пункт меню. external=true → обычная ссылка (вне react-router).
export type SidebarItem = {
  label: string;
  to: string;
  icon: ComponentType<{ className?: string }>;
  end?: boolean;
  external?: boolean;
};

// SidebarGroup — группа разделов с заголовком.
export type SidebarGroup = {
  label: string;
  items: SidebarItem[];
};

export type SidebarProps = {
  // brand — заголовок-бренд в шапке меню.
  brand: string;
  brandIcon: ComponentType<{ className?: string }>;
  groups: SidebarGroup[];
  // footer — необязательное содержимое внизу меню (например, текущий проект).
  footer?: React.ReactNode;
  // mobileOpen/onMobileClose — управление off-canvas режимом на узких экранах.
  mobileOpen?: boolean;
  onMobileClose?: () => void;
};

export function Sidebar({
  brand,
  brandIcon: BrandIcon,
  groups,
  footer,
  mobileOpen = false,
  onMobileClose,
}: SidebarProps) {
  // collapsed — свёрнутое (icon-only) состояние; инициализируется из localStorage.
  const [state, setState] = useState<SidebarState>(readSidebarState);
  const collapsed = state === "collapsed";

  // Сохраняем состояние при изменении (деградация при недоступности localStorage).
  useEffect(() => {
    writeSidebarState(state);
  }, [state]);

  const toggle = () => setState((prev) => (prev === "collapsed" ? "expanded" : "collapsed"));

  // navItem рендерит пункт меню с подсветкой активного маршрута и тултипом в
  // свёрнутом режиме.
  const linkClass = ({ isActive }: { isActive: boolean }) =>
    cn(
      "flex items-center gap-2.5 rounded-md px-3 py-2 text-sm font-medium transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring",
      collapsed && "justify-center px-0",
      isActive
        ? "bg-accent text-accent-foreground"
        : "text-muted-foreground hover:bg-accent/50 hover:text-foreground",
    );

  const content = (
    <>
      <div
        className={cn(
          "flex h-14 items-center gap-2 border-b border-border px-4",
          collapsed && "justify-center px-0",
        )}
      >
        <BrandIcon className="size-5 text-primary" />
        {!collapsed && <span className="text-sm font-semibold">{brand}</span>}
      </div>

      <nav
        className="flex flex-1 flex-col gap-3 overflow-y-auto p-2"
        aria-label="Основная навигация"
      >
        {groups.map((group) => (
          <div key={group.label} className="flex flex-col gap-1">
            {!collapsed && (
              <p className="px-3 pt-1 text-xs font-medium uppercase tracking-wide text-muted-foreground/70">
                {group.label}
              </p>
            )}
            {group.items.map((item) => {
              const Icon = item.icon;
              const label = item.label;
              if (item.external) {
                return (
                  <a
                    key={item.to}
                    href={item.to}
                    target="_blank"
                    rel="noreferrer"
                    title={collapsed ? label : undefined}
                    className={linkClass({ isActive: false })}
                  >
                    <Icon className="size-4" />
                    {!collapsed && label}
                  </a>
                );
              }
              return (
                <NavLink
                  key={item.to}
                  to={item.to}
                  end={item.end}
                  title={collapsed ? label : undefined}
                  className={linkClass}
                  onClick={onMobileClose}
                >
                  <Icon className="size-4" />
                  {!collapsed && label}
                </NavLink>
              );
            })}
          </div>
        ))}
      </nav>

      {footer && !collapsed && (
        <div className="border-t border-border p-3 text-xs text-muted-foreground">
          {footer}
        </div>
      )}

      <div className="border-t border-border p-2">
        <button
          type="button"
          onClick={toggle}
          aria-pressed={collapsed}
          aria-label={collapsed ? "Развернуть меню" : "Свернуть меню"}
          title={collapsed ? "Развернуть меню" : "Свернуть меню"}
          className={cn(
            "flex w-full items-center gap-2.5 rounded-md px-3 py-2 text-sm text-muted-foreground outline-none transition-colors hover:bg-accent/50 hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring",
            collapsed && "justify-center px-0",
          )}
        >
          {collapsed ? (
            <PanelLeftOpen className="size-4" />
          ) : (
            <>
              <PanelLeftClose className="size-4" />
              Свернуть
            </>
          )}
        </button>
      </div>
    </>
  );

  return (
    <>
      {/* Десктоп: статичная боковая панель, ширина зависит от свёрнутости. */}
      <aside
        data-collapsed={collapsed}
        className={cn(
          "hidden flex-shrink-0 flex-col border-r border-border md:flex",
          collapsed ? "w-16" : "w-56",
        )}
      >
        {content}
      </aside>

      {/* Мобильный off-canvas режим: затемнение + выезжающая панель. */}
      {mobileOpen && (
        <div className="fixed inset-0 z-40 md:hidden">
          <button
            type="button"
            aria-label="Закрыть меню"
            className="absolute inset-0 bg-black/50"
            onClick={onMobileClose}
          />
          <aside className="absolute left-0 top-0 flex h-full w-64 flex-col border-r border-border bg-background">
            <div className="flex justify-end p-2">
              <button
                type="button"
                aria-label="Закрыть меню"
                onClick={onMobileClose}
                className="rounded-md p-1.5 text-muted-foreground hover:bg-accent/50 hover:text-foreground"
              >
                <X className="size-4" />
              </button>
            </div>
            {content}
          </aside>
        </div>
      )}
    </>
  );
}
