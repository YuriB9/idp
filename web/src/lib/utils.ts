import { type ClassValue, clsx } from "clsx";
import { twMerge } from "tailwind-merge";

// cn объединяет классы Tailwind с разрешением конфликтов (clsx + tailwind-merge).
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
