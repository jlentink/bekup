import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

/** A dashed "nothing filed here yet" panel with guidance and an action. */
export function EmptyState({
  icon: Icon,
  title,
  children,
  action,
  className,
}: {
  icon: LucideIcon;
  title: string;
  children?: ReactNode;
  action?: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex flex-col items-start gap-3 rounded-lg border border-dashed border-border-strong/50 p-8",
        className,
      )}
    >
      <span
        aria-hidden="true"
        className="inline-flex size-9 items-center justify-center rounded-md border bg-surface text-muted-foreground"
      >
        <Icon className="size-4" />
      </span>
      <div className="flex max-w-prose flex-col gap-1">
        <p className="font-medium">{title}</p>
        {children ? <div className="text-sm text-muted-foreground">{children}</div> : null}
      </div>
      {action}
    </div>
  );
}
