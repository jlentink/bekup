import { LockOpen, ShieldOff, type LucideIcon } from "lucide-react";
import type { ComponentProps } from "react";

import { cn } from "@/lib/utils";

import type { SecurityFlag } from "./status";

const FLAG_META: Record<SecurityFlag, { label: string; description: string; icon: LucideIcon }> = {
  unencrypted: {
    label: "Unencrypted",
    description: "Backup encryption was turned off by an administrator.",
    icon: LockOpen,
  },
  "insecure-transport": {
    label: "Insecure transport",
    description: "TLS verification was relaxed for this connection by an administrator.",
    icon: ShieldOff,
  },
};

interface SecurityBadgeProps extends Omit<ComponentProps<"span">, "children"> {
  flag: SecurityFlag;
}

/**
 * Permanent marker for a weakened security setting (D-001). The hazard hatch
 * is reserved for these badges and must not be used decoratively.
 */
export function SecurityBadge({ flag, className, ...props }: SecurityBadgeProps) {
  const meta = FLAG_META[flag];
  const Icon = meta.icon;
  return (
    <span
      data-slot="security-badge"
      data-flag={flag}
      title={meta.description}
      className={cn(
        "hatch-insecure inline-flex h-6 items-center gap-1.5 rounded-sm border border-status-insecure/60 px-2",
        "font-mono text-2xs font-semibold tracking-[0.08em] text-status-insecure-fg uppercase",
        className,
      )}
      {...props}
    >
      <Icon aria-hidden="true" className="size-3.5 shrink-0" />
      {meta.label}
      <span className="sr-only">: {meta.description}</span>
    </span>
  );
}
