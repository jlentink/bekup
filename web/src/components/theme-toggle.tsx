import { Monitor, Moon, Sun, type LucideIcon } from "lucide-react";
import { useId } from "react";

import { useTheme, type ThemePreference } from "@/lib/theme";
import { cn } from "@/lib/utils";

const OPTIONS: readonly { value: ThemePreference; label: string; icon: LucideIcon }[] = [
  { value: "light", label: "Light", icon: Sun },
  { value: "dark", label: "Dark", icon: Moon },
  { value: "system", label: "System", icon: Monitor },
];

/**
 * Three-way theme switch built on native radio inputs (arrow keys, screen
 * readers and forms work without extra ARIA). The choice is persisted in
 * localStorage; "system" follows prefers-color-scheme.
 */
export function ThemeToggle({ className }: { className?: string }) {
  const { preference, setPreference } = useTheme();
  const name = useId();
  return (
    <fieldset
      className={cn(
        "inline-flex h-8 items-center gap-0.5 rounded-md border bg-surface-sunken p-0.5",
        className,
      )}
    >
      <legend className="sr-only">Color theme</legend>
      {OPTIONS.map(({ value, label, icon: Icon }) => (
        <label
          key={value}
          title={label}
          className={cn(
            "relative inline-flex size-7 cursor-pointer items-center justify-center rounded-[calc(var(--radius)-2px)] text-muted-foreground transition-colors",
            "hover:text-foreground has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-ring",
            preference === value && "bg-surface text-foreground shadow-raised",
          )}
        >
          <input
            type="radio"
            name={name}
            value={value}
            checked={preference === value}
            onChange={() => {
              setPreference(value);
            }}
            className="sr-only"
          />
          <Icon aria-hidden="true" className="size-4" />
          <span className="sr-only">{label}</span>
        </label>
      ))}
    </fieldset>
  );
}
