// Раздвигающееся левое меню портала (collapsible sidebar, ADR-0017). Группировка
// разделов, подсветка активного маршрута (react-router → aria-current), свёрнутый
// icon-only режим с тултипами, сохранение состояния в localStorage, клавиатурная
// навигация и ARIA-роли. Поддерживает ВЛОЖЕННЫЕ (collapsible) подменю: пункт с
// дочерними под-пунктами разворачивается/сворачивается (aria-expanded на родителе),
// авто-раскрывается на активном дочернем маршруте, сохраняет состояние раскрытия в
// localStorage. В свёрнутом icon-only режиме подменю доступно поповером при
// наведении/фокусе (клавиатура — через focus-within). На узких экранах — off-canvas
// режим. Недоступность localStorage не ломает меню (деградация к дефолту).
import { useEffect, useMemo, useState, type ComponentType } from "react";
import { NavLink, useLocation } from "react-router-dom";
import { ChevronRight, PanelLeftClose, PanelLeftOpen, X } from "lucide-react";

import { cn } from "@/lib/utils";
import {
  readSidebarState,
  readSidebarSubmenus,
  writeSidebarState,
  writeSidebarSubmenus,
  type SidebarState,
} from "@/lib/ui-state";

// SidebarItem — пункт меню. external=true → обычная ссылка (вне react-router).
// children — дочерние под-пункты (раскрывающееся подменю).
export type SidebarItem = {
  label: string;
  to: string;
  icon: ComponentType<{ className?: string }>;
  end?: boolean;
  external?: boolean;
  children?: SidebarItem[];
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

// isChildActive проверяет, активен ли маршрут под-пункта для текущего пути
// (учитывая вложенные сегменты, например /iam/roles/:role).
function isChildActive(pathname: string, to: string): boolean {
  return pathname === to || pathname.startsWith(to + "/");
}

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

  // openKeys — вручную раскрытые подменю (ключ = `to` родителя); из localStorage.
  const [openKeys, setOpenKeys] = useState<string[]>(readSidebarSubmenus);
  const location = useLocation();

  // Сохраняем состояние при изменении (деградация при недоступности localStorage).
  useEffect(() => {
    writeSidebarState(state);
  }, [state]);

  useEffect(() => {
    writeSidebarSubmenus(openKeys);
  }, [openKeys]);

  const toggle = () => setState((prev) => (prev === "collapsed" ? "expanded" : "collapsed"));

  const toggleSubmenu = (key: string) =>
    setOpenKeys((prev) => (prev.includes(key) ? prev.filter((k) => k !== key) : [...prev, key]));

  // Эффективно раскрытые подменю: сохранённые ∪ содержащие активный маршрут (активный
  // под-пункт всегда виден, ручное сворачивание не прячет активный маршрут).
  const effectiveOpen = useMemo(() => {
    const set = new Set(openKeys);
    for (const group of groups) {
      for (const item of group.items) {
        if (
          item.children?.some((c) => isChildActive(location.pathname, c.to))
        ) {
          set.add(item.to);
        }
      }
    }
    return set;
  }, [openKeys, groups, location.pathname]);

  // linkClass — общий стиль пункта/ссылки с подсветкой активного маршрута.
  const linkClass = ({ isActive }: { isActive: boolean }) =>
    cn(
      "flex items-center gap-2.5 rounded-md px-3 py-2 text-sm font-medium outline-none motion-safe:transition-colors focus-visible:ring-2 focus-visible:ring-ring",
      collapsed && "justify-center px-0",
      isActive
        ? "bg-accent text-accent-foreground"
        : "text-muted-foreground hover:bg-accent/50 hover:text-foreground",
    );

  // renderLeaf рендерит обычный пункт (без детей): внешнюю ссылку или NavLink.
  // depth=1 — под-пункт подменю (меньший отступ слева в развёрнутом режиме).
  const renderLeaf = (item: SidebarItem, depth = 0) => {
    const Icon = item.icon;
    if (item.external) {
      return (
        <a
          key={item.to}
          href={item.to}
          target="_blank"
          rel="noreferrer"
          title={collapsed ? item.label : undefined}
          className={linkClass({ isActive: false })}
        >
          <Icon className="size-4" />
          {!collapsed && item.label}
        </a>
      );
    }
    return (
      <NavLink
        key={item.to}
        to={item.to}
        end={item.end}
        title={collapsed ? item.label : undefined}
        className={({ isActive }) =>
          cn(linkClass({ isActive }), !collapsed && depth > 0 && "pl-9")
        }
        onClick={onMobileClose}
      >
        {/* У под-пунктов в развёрнутом режиме иконку прячем ради выравнивания. */}
        {(collapsed || depth === 0) && <Icon className="size-4" />}
        {!collapsed && item.label}
      </NavLink>
    );
  };

  // renderParent рендерит пункт с подменю: раскрывающийся в развёрнутом режиме и
  // поповер при наведении/фокусе в свёрнутом icon-only режиме.
  const renderParent = (item: SidebarItem) => {
    const Icon = item.icon;
    const children = item.children ?? [];
    const childActive = children.some((c) => isChildActive(location.pathname, c.to));
    const submenuId = `submenu-${item.to}`;

    if (collapsed) {
      // Свёрнутый режим: иконка-родитель + поповер с под-пунктами. Поповер виден при
      // наведении и при фокусе внутри обёртки (focus-within), что делает под-пункты
      // достижимыми с клавиатуры (фокус на родителе → поповер виден → таб по ссылкам).
      return (
        <div key={item.to} className="group/sm relative">
          <button
            type="button"
            aria-haspopup="menu"
            aria-label={item.label}
            title={item.label}
            className={cn(
              linkClass({ isActive: childActive }),
              "w-full",
            )}
          >
            <Icon className="size-4" />
          </button>
          <div
            role="menu"
            aria-label={item.label}
            className={cn(
              "invisible absolute left-full top-0 z-50 ml-1 flex w-48 flex-col gap-1 rounded-md border border-border bg-background p-1 opacity-0 shadow-md",
              "group-hover/sm:visible group-hover/sm:opacity-100 group-focus-within/sm:visible group-focus-within/sm:opacity-100",
            )}
          >
            <p className="px-2 py-1 text-xs font-medium text-muted-foreground">{item.label}</p>
            {children.map((c) => {
              const ChildIcon = c.icon;
              return (
                <NavLink
                  key={c.to}
                  to={c.to}
                  end={c.end}
                  role="menuitem"
                  className={({ isActive }) =>
                    cn(
                      "flex items-center gap-2.5 rounded-md px-2 py-1.5 text-sm font-medium outline-none motion-safe:transition-colors focus-visible:ring-2 focus-visible:ring-ring",
                      isActive
                        ? "bg-accent text-accent-foreground"
                        : "text-muted-foreground hover:bg-accent/50 hover:text-foreground",
                    )
                  }
                  onClick={onMobileClose}
                >
                  <ChildIcon className="size-4" />
                  {c.label}
                </NavLink>
              );
            })}
          </div>
        </div>
      );
    }

    // Развёрнутый режим: кнопка-родитель раскрывает/сворачивает подменю (навигация —
    // по под-пунктам). Активный дочерний маршрут авто-раскрывает подменю.
    const open = effectiveOpen.has(item.to);
    return (
      <div key={item.to} className="flex flex-col gap-1">
        <button
          type="button"
          aria-expanded={open}
          aria-controls={submenuId}
          onClick={() => toggleSubmenu(item.to)}
          className={cn(
            linkClass({ isActive: childActive && !open }),
            "w-full justify-between",
          )}
        >
          <span className="flex items-center gap-2.5">
            <Icon className="size-4" />
            {item.label}
          </span>
          <ChevronRight
            className={cn("size-4 motion-safe:transition-transform", open && "rotate-90")}
          />
        </button>
        {open && (
          <div id={submenuId} className="flex flex-col gap-1">
            {children.map((c) => renderLeaf(c, 1))}
          </div>
        )}
      </div>
    );
  };

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
            {group.items.map((item) =>
              item.children && item.children.length > 0
                ? renderParent(item)
                : renderLeaf(item),
            )}
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
            "flex w-full items-center gap-2.5 rounded-md px-3 py-2 text-sm text-muted-foreground outline-none motion-safe:transition-colors hover:bg-accent/50 hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring",
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
