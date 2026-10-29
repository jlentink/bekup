import type { ComponentProps } from "react";

import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

import { normalizeTotp, TOTP_DIGITS } from "./code-input-utils";

/**
 * A one-time-code field: one native input (so autofill, paste and screen
 * readers work as usual), set in large tabular mono, with a row of ledger
 * ticks below that fill as digits are typed.
 */
export function CodeInput({
  value,
  onChange,
  className,
  ...props
}: Omit<ComponentProps<"input">, "value" | "onChange"> & {
  value: string;
  onChange: (value: string) => void;
}) {
  const digits = normalizeTotp(value);
  return (
    <div className="flex flex-col gap-2">
      <Input
        {...props}
        value={digits}
        onChange={(e) => {
          onChange(normalizeTotp(e.target.value));
        }}
        inputMode="numeric"
        autoComplete="one-time-code"
        pattern="[0-9]*"
        maxLength={TOTP_DIGITS + 2}
        spellCheck={false}
        className={cn(
          "h-12 text-center font-mono text-2xl tracking-[0.5em] tabular-nums md:text-2xl",
          className,
        )}
      />
      <div aria-hidden="true" className="grid grid-cols-6 gap-1.5 px-1">
        {Array.from({ length: TOTP_DIGITS }, (_, i) => (
          <span
            key={i}
            className={cn(
              "h-0.5 rounded-full bg-border transition-colors duration-150",
              i < digits.length && "bg-primary",
              i === 2 && "mr-1.5",
            )}
          />
        ))}
      </div>
    </div>
  );
}
