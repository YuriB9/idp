// Переключатель темы (светлая/тёмная) для шапки портала. Иконка отражает тему, на
// которую переключит клик: в тёмной показываем солнце (→ светлая), в светлой —
// луну (→ тёмная).
import { Moon, Sun } from "lucide-react";

import { Button } from "@/components/ui/button";
import { useTheme } from "@/lib/theme";

export function ThemeToggle() {
  const { theme, toggleTheme } = useTheme();
  const toLight = theme === "dark";

  return (
    <Button
      variant="ghost"
      size="icon"
      onClick={toggleTheme}
      aria-label={toLight ? "Включить светлую тему" : "Включить тёмную тему"}
      title={toLight ? "Светлая тема" : "Тёмная тема"}
    >
      {toLight ? <Sun className="size-4" /> : <Moon className="size-4" />}
    </Button>
  );
}
