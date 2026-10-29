import { ArrowUpRight, TriangleAlert } from "lucide-react";
import { useId, type ReactNode } from "react";
import { Link } from "react-router";

import { Skeleton } from "@/components/ui/skeleton";
import { userMessage } from "@/lib/api-error";
import { cn } from "@/lib/utils";

/**
 * One dashboard tile: a ledger folio with a numbered mono eyebrow, a title,
 * an optional live marker and a link to the detail page.
 */
export function Tile({
  folio,
  title,
  to,
  linkLabel,
  aside,
  children,
  className,
  testId,
}: {
  folio: string;
  title: string;
  to?: string;
  linkLabel?: string;
  aside?: ReactNode;
  children: ReactNode;
  className?: string;
  testId?: string;
}) {
  const id = useId();
  return (
    <section
      aria-labelledby={id}
      data-testid={testId}
      className={cn(
        "dash-tile dash-reveal flex min-w-0 flex-col rounded-lg border bg-card",
        className,
      )}
    >
      <header className="flex items-baseline gap-3 border-b border-rule px-5 pt-4 pb-3">
        <span aria-hidden="true" className="font-mono text-2xs text-muted-foreground">
          {folio}
        </span>
        <h2 id={id} className="font-display text-xl leading-none tracking-tight">
          {title}
        </h2>
        <div className="ml-auto flex items-center gap-3">
          {aside}
          {to ? (
            <Link
              to={to}
              className="inline-flex items-center gap-1 rounded-sm font-mono text-2xs tracking-[0.1em] text-muted-foreground uppercase underline-offset-4 hover:text-foreground hover:underline"
            >
              {linkLabel ?? "View all"}
              <span className="sr-only">: {title}</span>
              <ArrowUpRight aria-hidden="true" className="size-3" />
            </Link>
          ) : null}
        </div>
      </header>
      <div className="flex flex-1 flex-col px-5 py-4">{children}</div>
    </section>
  );
}

/** Placeholder rows while a tile loads. */
export function TileSkeleton({ rows = 3, label }: { rows?: number; label: string }) {
  return (
    <div role="status" aria-label={label} className="flex flex-col gap-2.5">
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} className="h-6 w-full" />
      ))}
    </div>
  );
}

/** A tile that could not load: a short, fixed message (never raw server output). */
export function TileError({ error }: { error: unknown }) {
  return (
    <p className="flex items-start gap-2 text-sm text-status-failed-fg">
      <TriangleAlert aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
      {userMessage(error)}
    </p>
  );
}

/** A quiet "nothing here" line inside a tile. */
export function TileEmpty({ icon, children }: { icon: ReactNode; children: ReactNode }) {
  return (
    <p className="flex items-center gap-2 py-2 text-sm text-muted-foreground">
      {icon}
      {children}
    </p>
  );
}

/** The live marker of the page's activity stream (D-197). */
export function LiveMarker({ on }: { on: boolean }) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 font-mono text-2xs tracking-[0.12em] uppercase",
        on ? "text-status-running-fg" : "text-muted-foreground",
      )}
      data-testid="live-marker"
      data-live={on ? "true" : "false"}
    >
      <span
        aria-hidden="true"
        className={cn(
          "inline-block size-1.5 rounded-full",
          on ? "running-pulse bg-status-running" : "bg-status-neutral",
        )}
      />
      {on ? "Live" : "Offline"}
    </span>
  );
}
