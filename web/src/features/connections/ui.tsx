import { useId, type ReactNode } from "react";

import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";

/**
 * Small building blocks shared by the connection and destination pages
 * (both task 6.3/6.4): ledger rows and skeletons.
 */

/** A ledger panel: heading column on the left, content on the right. */
export function LedgerSection({
  title,
  lead,
  aside,
  actions,
  children,
  className,
}: {
  title: string;
  lead?: ReactNode;
  aside?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  const headingId = useId();
  return (
    <section
      aria-labelledby={headingId}
      className={cn("@container border-b border-rule py-7 last:border-b-0", className)}
    >
      <div className="grid gap-x-10 gap-y-4 @3xl:grid-cols-[minmax(0,16rem)_minmax(0,1fr)]">
        <div className="flex flex-col gap-2">
          <h2 id={headingId} className="text-[0.9375rem] font-semibold tracking-tight">
            {title}
          </h2>
          {lead ? <p className="text-sm text-muted-foreground">{lead}</p> : null}
          {aside ? <div className="flex flex-wrap gap-2 pt-1">{aside}</div> : null}
        </div>
        <div className="flex min-w-0 flex-col gap-4">
          {children}
          {actions ? <div className="flex flex-wrap gap-2">{actions}</div> : null}
        </div>
      </div>
    </section>
  );
}

/** Term/value rows; values that operators compare are mono. */
export function Facts({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <dl
      className={cn(
        "grid grid-cols-[minmax(8rem,auto)_minmax(0,1fr)] gap-x-6 gap-y-2.5 text-sm",
        className,
      )}
    >
      {children}
    </dl>
  );
}

export function Fact({
  term,
  children,
  mono = false,
}: {
  term: string;
  children: ReactNode;
  mono?: boolean;
}) {
  return (
    <>
      <dt className="eyebrow pt-0.5">{term}</dt>
      <dd className={cn("min-w-0 break-words", mono && "font-mono text-xs leading-5 break-all")}>
        {children}
      </dd>
    </>
  );
}

/** Table placeholder rows while a list loads. */
export function TableSkeleton({ rows = 4, label }: { rows?: number; label: string }) {
  return (
    <div role="status" aria-label={label} className="flex flex-col gap-0 rounded-lg border">
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="flex items-center gap-6 border-b px-4 py-3.5 last:border-b-0">
          <Skeleton className="h-4 w-40" />
          <Skeleton className="h-4 w-24" />
          <Skeleton className="h-4 w-32" />
          <Skeleton className="ml-auto h-5 w-20" />
        </div>
      ))}
    </div>
  );
}

/** Detail page placeholder. */
export function DetailSkeleton({ label }: { label: string }) {
  return (
    <div role="status" aria-label={label} className="flex flex-col gap-8">
      <Skeleton className="h-10 w-72" />
      {Array.from({ length: 3 }, (_, i) => (
        <div key={i} className="grid gap-6 border-b py-6 md:grid-cols-[16rem_1fr]">
          <Skeleton className="h-5 w-32" />
          <div className="flex flex-col gap-3">
            <Skeleton className="h-4 w-full" />
            <Skeleton className="h-4 w-2/3" />
          </div>
        </div>
      ))}
    </div>
  );
}

/** Field group inside a form: a mono legend and a hairline. */
export function FormGroup({
  legend,
  lead,
  children,
}: {
  legend: string;
  lead?: ReactNode;
  children: ReactNode;
}) {
  return (
    <fieldset className="grid gap-x-10 gap-y-4 border-b border-rule py-7 last:border-b-0 md:grid-cols-[minmax(0,14rem)_minmax(0,1fr)]">
      <div className="flex flex-col gap-1.5">
        <legend className="eyebrow float-left">{legend}</legend>
        {lead ? <p className="clear-left text-sm text-muted-foreground">{lead}</p> : null}
      </div>
      <div className="flex min-w-0 flex-col gap-5">{children}</div>
    </fieldset>
  );
}
