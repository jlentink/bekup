import { useMutation, useQuery } from "@apollo/client/react";
import { CircleCheck, ShieldCheck, Undo2 } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { Tag, type Tone } from "@/components/status/tag";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { isStepUpCancelled, useStepUp } from "@/features/auth/use-step-up";
import type {
  RiskAcceptanceFilter,
  RiskAcceptanceStatus,
  RiskAcceptancesQuery as RiskAcceptancesQueryData,
} from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { formatDateTime, formatRelative } from "@/lib/format";
import { SecurityRisksQuery } from "@/lib/operations";
import { cn } from "@/lib/utils";

import { AcceptRiskDialog } from "./accept-dialog";
import { T } from "./messages";
import { subjectPath, type RiskRef } from "./model";
import { RevokeRiskAcceptanceMutation, RiskAcceptancesQuery } from "./operations";

type Row = RiskAcceptancesQueryData["riskAcceptances"][number];

const STATUS_TONE: Record<RiskAcceptanceStatus, Tone> = {
  ACTIVE: "primary",
  EXPIRED: "neutral",
  REVOKED: "neutral",
  ENDED: "neutral",
};

const FILTERS: readonly RiskAcceptanceFilter[] = ["ACTIVE", "ENDED", "ALL"];

/**
 * /risks: the ledger of accepted security risks (D-228) and, on top, the
 * open risks the caller may accept. Every role reads it (the server scopes
 * it: owners see their jobs' acceptances only); accepting and revoking need
 * a step-up and are offered only where the server says the caller may.
 */
export function AcceptedRisksPage() {
  const [filter, setFilter] = useState<RiskAcceptanceFilter>("ACTIVE");
  const list = useQuery(RiskAcceptancesQuery, {
    variables: { filter, first: 200 },
    fetchPolicy: "cache-and-network",
  });
  const risks = useQuery(SecurityRisksQuery, { fetchPolicy: "cache-and-network" });
  const [accepting, setAccepting] = useState<RiskRef | null>(null);
  const [revoking, setRevoking] = useState<Row | null>(null);
  const { withStepUp } = useStepUp();
  const [revoke] = useMutation(RevokeRiskAcceptanceMutation, {
    refetchQueries: [RiskAcceptancesQuery, SecurityRisksQuery],
  });
  const open = (risks.data?.securityRisks ?? []).filter((r) => !r.acceptance && r.canAccept);
  const mayRevoke = new Set(
    (risks.data?.securityRisks ?? [])
      .filter((r) => r.canAccept && r.acceptance)
      .map((r) => r.acceptance?.id),
  );
  const rows = list.data?.riskAcceptances ?? [];

  return (
    <div className="flex flex-col gap-8">
      <PageHeader eyebrow={T.page.eyebrow} title={T.page.title} description={T.page.lead} />

      {open.length > 0 ? (
        <section aria-labelledby="open-risks" className="flex flex-col gap-3">
          <div className="flex items-baseline justify-between gap-4">
            <h2 id="open-risks" className="eyebrow text-status-insecure-fg">
              {T.page.openHeading}
            </h2>
            <span className="text-xs text-muted-foreground">{T.page.open(open.length)}</span>
          </div>
          <ul className="grid gap-2 md:grid-cols-2">
            {open.map((r) => (
              <li
                key={`${r.kind}:${r.conditionId}`}
                className="hatch-insecure flex items-center justify-between gap-3 rounded-md border border-status-insecure/50 px-4 py-3"
              >
                <div className="flex min-w-0 flex-col gap-0.5">
                  <span className="font-mono text-2xs font-semibold tracking-[0.08em] text-status-insecure-fg uppercase">
                    {T.kinds[r.kind]} · {r.subjectName ?? T.instance}
                  </span>
                  <span className="truncate text-sm" title={r.condition}>
                    {r.condition}
                  </span>
                </div>
                <Button
                  size="sm"
                  variant="outline"
                  className="shrink-0 bg-surface"
                  onClick={() => {
                    setAccepting(r);
                  }}
                  aria-label={`${T.accept}: ${T.kinds[r.kind]} ${r.subjectName ?? ""}`.trim()}
                >
                  <ShieldCheck aria-hidden="true" /> {T.accept}
                </Button>
              </li>
            ))}
          </ul>
        </section>
      ) : null}

      <section aria-labelledby="ledger" className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h2 id="ledger" className="eyebrow">
            {T.page.ledgerHeading}
          </h2>
          <div
            role="radiogroup"
            aria-label={T.page.filterLabel}
            className="flex rounded-md border bg-surface p-0.5"
          >
            {FILTERS.map((f) => (
              <button
                key={f}
                type="button"
                role="radio"
                aria-checked={filter === f}
                className={cn(
                  "rounded-sm px-3 py-1 text-xs font-medium focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none",
                  filter === f
                    ? "bg-accent text-foreground"
                    : "text-muted-foreground hover:text-foreground",
                )}
                onClick={() => {
                  setFilter(f);
                }}
              >
                {T.page.filters[f]}
              </button>
            ))}
          </div>
        </div>
        {list.loading && !list.data ? (
          <Skeleton className="h-40 w-full" />
        ) : list.error && !list.data ? (
          <Alert variant="destructive">
            <AlertDescription>{userMessage(list.error)}</AlertDescription>
          </Alert>
        ) : rows.length === 0 ? (
          <EmptyState icon={CircleCheck} title={T.page.empty}>
            {filter === "ENDED" ? T.page.emptyEnded : T.page.emptyActive}
          </EmptyState>
        ) : (
          <ol className="flex flex-col divide-y divide-rule rounded-md border bg-surface">
            {rows.map((a) => (
              <LedgerEntry
                key={a.id}
                a={a}
                canRevoke={a.status === "ACTIVE" && mayRevoke.has(a.id)}
                onRevoke={() => {
                  setRevoking(a);
                }}
              />
            ))}
          </ol>
        )}
      </section>

      {accepting ? (
        <AcceptRiskDialog
          risk={accepting}
          onOpenChange={(o) => {
            if (!o) setAccepting(null);
          }}
        />
      ) : null}
      <ConfirmDialog
        open={revoking !== null}
        onOpenChange={(o) => {
          if (!o) setRevoking(null);
        }}
        title={T.revokeTitle}
        description={revoking ? T.revokeLead(T.kinds[revoking.kind]) : ""}
        confirmLabel={T.revoke}
        destructive
        onConfirm={async () => {
          if (!revoking) return;
          try {
            await withStepUp(() => revoke({ variables: { id: revoking.id } }));
            toast.success(T.revoked);
          } catch (err) {
            if (isStepUpCancelled(err)) return;
            toast.error(userMessage(err));
            throw err;
          }
        }}
      />
    </div>
  );
}

/** One acceptance: what, on what, why, by whom and until when. */
function LedgerEntry({
  a,
  canRevoke,
  onRevoke,
}: {
  a: Row;
  canRevoke: boolean;
  onRevoke: () => void;
}) {
  const active = a.status === "ACTIVE";
  const path = subjectPath(a);
  const subject = a.subjectName ?? T.instance;
  const by = a.createdByMe
    ? T.byYou
    : (a.createdBy?.displayName ?? a.createdBy?.email ?? T.unknownPerson);
  return (
    <li
      data-testid="risk-acceptance"
      className={cn(
        "grid gap-3 px-4 py-4 sm:grid-cols-[minmax(0,1.3fr)_minmax(0,1.4fr)_auto] sm:items-start",
        !active && "text-muted-foreground",
      )}
    >
      <div className="flex min-w-0 flex-col gap-1.5">
        <div className="flex flex-wrap items-center gap-2">
          <span
            className={cn(
              "inline-flex h-6 items-center gap-1.5 rounded-sm border px-2 font-mono text-2xs font-semibold tracking-[0.08em] uppercase",
              active
                ? "hatch-accepted border-dashed border-status-insecure/35 text-foreground"
                : "border-border text-muted-foreground",
            )}
          >
            {active ? <CircleCheck aria-hidden="true" className="size-3.5" /> : null}
            {T.kinds[a.kind]}
          </span>
          <Tag tone={STATUS_TONE[a.status]} compact>
            {T.statuses[a.status]}
          </Tag>
        </div>
        <p className="text-sm text-foreground">{a.condition}</p>
        <p className="text-xs">
          {path && !a.subjectDeleted ? (
            <Link to={path} className="underline-offset-2 hover:underline">
              {subject}
            </Link>
          ) : (
            <span className={a.subjectDeleted ? "line-through" : undefined}>{subject}</span>
          )}
          {a.subjectDeleted ? <span className="sr-only"> ({T.deleted})</span> : null}
        </p>
      </div>
      <blockquote className="border-l-2 border-rule pl-3 text-sm italic">
        {T.quoted(a.reason)}
        <footer className="mt-1 text-xs text-muted-foreground not-italic">
          {by} · {formatDateTime(a.createdAt)}
        </footer>
      </blockquote>
      <div className="flex flex-col items-start gap-2 sm:items-end">
        <dl className="text-left text-xs sm:text-right">
          <dt className="sr-only">{T.columns.until}</dt>
          <dd className="font-mono text-foreground">
            {a.endedAt ? (
              <>
                {a.endReason ? T.endReasons[a.endReason] : T.statuses[a.status]} ·{" "}
                {formatDateTime(a.endedAt)}
              </>
            ) : a.expiresAt ? (
              <span title={formatDateTime(a.expiresAt)}>
                {T.columns.until} {formatDateTime(a.expiresAt)}
                <span className="block text-muted-foreground">{formatRelative(a.expiresAt)}</span>
              </span>
            ) : (
              <span className="text-status-insecure-fg">{T.noEnd}</span>
            )}
          </dd>
        </dl>
        {canRevoke ? (
          <Button
            size="sm"
            variant="ghost"
            className="text-destructive hover:bg-status-failed-soft hover:text-destructive"
            onClick={onRevoke}
            aria-label={`${T.revoke}: ${T.kinds[a.kind]} ${a.subjectName ?? ""}`.trim()}
          >
            <Undo2 aria-hidden="true" /> {T.revoke}
          </Button>
        ) : null}
      </div>
    </li>
  );
}
