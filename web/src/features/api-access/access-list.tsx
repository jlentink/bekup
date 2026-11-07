import { useMutation } from "@apollo/client/react";
import { Ban, Pencil } from "lucide-react";
import { useState, type ReactNode } from "react";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { SecurityBadge } from "@/components/status/security-badge";
import { Tag, type Tone } from "@/components/status/tag";
import { Button } from "@/components/ui/button";
import { reportActionError, useStepUp } from "@/features/auth/use-step-up";
import type { ApiTokensQuery as ApiTokensResult } from "@/graphql/gen/graphql";
import { formatDateTime, formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

import { AddressesDialog } from "./addresses-dialog";
import { T } from "./messages";
import { daysUntil, tokenState, type TokenState } from "./model";
import { ApiTokensQuery, RevokeApiTokenMutation } from "./operations";

export type ApiTokenRow = ApiTokensResult["apiTokens"][number];

const STATE_TONE: Record<TokenState, Tone> = {
  active: "success",
  expiring: "warning",
  expired: "neutral",
  revoked: "neutral",
};

/**
 * API tokens with their scope, expiry, last sighting and address list, and
 * the actions (edit addresses, revoke; both need a step-up). With jobId
 * (the job's panel) the scope shows as "+ N other jobs"; without it (the
 * admin page) the job names are listed. Revoked and expired tokens are
 * folded away below the live ones.
 */
export function AccessList({
  tokens,
  jobId,
}: {
  tokens: readonly ApiTokenRow[];
  jobId?: string | undefined;
}) {
  const [editing, setEditing] = useState<ApiTokenRow | null>(null);
  const [revoking, setRevoking] = useState<ApiTokenRow | null>(null);
  const { withStepUp } = useStepUp();
  const [revoke] = useMutation(RevokeApiTokenMutation, { refetchQueries: [ApiTokensQuery] });
  // One clock reading per mount: states and relative times agree.
  const [now] = useState(() => Date.now());
  const live = tokens.filter((t) => {
    const s = tokenState(t, now);
    return s === "active" || s === "expiring";
  });
  const past = tokens.filter((t) => !live.includes(t));

  const item = (t: ApiTokenRow) => (
    <TokenItem
      key={t.id}
      token={t}
      jobId={jobId}
      now={now}
      onEdit={() => {
        setEditing(t);
      }}
      onRevoke={() => {
        setRevoking(t);
      }}
    />
  );

  return (
    <div className="flex flex-col gap-3">
      {live.length > 0 ? (
        <ul aria-label={T.list.label} className="flex flex-col gap-3">
          {live.map(item)}
        </ul>
      ) : null}
      {past.length > 0 ? (
        <details className="group rounded-md border px-3 py-2">
          <summary className="cursor-pointer text-sm text-muted-foreground">
            {T.panel.revokedHeading(past.length)}
          </summary>
          <ul className="mt-3 flex flex-col gap-3">{past.map(item)}</ul>
        </details>
      ) : null}

      {editing ? (
        <AddressesDialog
          key={editing.id}
          token={editing}
          onOpenChange={(o) => {
            if (!o) setEditing(null);
          }}
        />
      ) : null}
      <ConfirmDialog
        open={revoking !== null}
        onOpenChange={(o) => {
          if (!o) setRevoking(null);
        }}
        title={T.list.revokeTitle(revoking?.name ?? "")}
        description={T.list.revokeLead}
        confirmLabel={T.list.revokeConfirm}
        destructive
        onConfirm={async () => {
          if (!revoking) return;
          try {
            await withStepUp(() => revoke({ variables: { id: revoking.id } }));
            toast.success(T.list.revokedToast);
          } catch (err) {
            reportActionError(err);
            throw err;
          }
        }}
      />
    </div>
  );
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <dt className="eyebrow">{label}</dt>
      <dd className="min-w-0 text-sm">{children}</dd>
    </div>
  );
}

function TokenItem({
  token: t,
  jobId,
  now,
  onEdit,
  onRevoke,
}: {
  token: ApiTokenRow;
  jobId?: string | undefined;
  now: number;
  onEdit: () => void;
  onRevoke: () => void;
}) {
  const state = tokenState(t, now);
  const ended = state === "revoked" || state === "expired";
  const otherJobs = t.jobs.filter((j) => j.id !== jobId);
  const creator = t.createdByMe
    ? T.list.createdByYou
    : t.createdBy
      ? t.createdBy.displayName || t.createdBy.email
      : null;
  return (
    <li
      data-testid="api-token"
      className={cn(
        "flex flex-col gap-3 rounded-md border bg-surface px-4 py-3",
        ended && "opacity-80",
      )}
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex min-w-0 flex-col gap-1">
          <span className="truncate font-medium">{t.name}</span>
          <span className="font-mono text-2xs text-muted-foreground">
            <span className="sr-only">{T.list.prefix}: </span>
            {t.prefix}…
          </span>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Tag
            tone={STATE_TONE[state]}
            title={
              state === "expiring" && t.expiresAt
                ? T.list.expiringTitle(daysUntil(t.expiresAt, now))
                : undefined
            }
          >
            {T.list.states[state]}
          </Tag>
          {!t.revokedAt ? (
            <>
              <Button
                variant="ghost"
                size="sm"
                onClick={onEdit}
                aria-label={`${T.list.editAddresses}: ${t.name}`}
              >
                <Pencil aria-hidden="true" /> {T.list.editAddresses}
              </Button>
              <Button
                variant="ghost"
                size="sm"
                className="text-destructive hover:bg-status-failed-soft hover:text-destructive"
                onClick={onRevoke}
                aria-label={`${T.list.revoke}: ${t.name}`}
              >
                <Ban aria-hidden="true" /> {T.list.revoke}
              </Button>
            </>
          ) : null}
        </div>
      </div>
      <dl className="grid gap-x-6 gap-y-3 sm:grid-cols-2 xl:grid-cols-4">
        <Fact label={T.list.jobs}>
          {jobId ? (
            <span title={otherJobs.map((j) => j.name).join(", ") || undefined}>
              {T.list.otherJobs(otherJobs.length + t.hiddenJobCount)}
            </span>
          ) : (
            <ul className="flex flex-col">
              {t.jobs.map((j) => (
                <li key={j.id} className={j.deleted ? "text-muted-foreground line-through" : ""}>
                  {j.name}
                  {j.deleted ? <span className="sr-only"> ({T.list.deletedJob})</span> : null}
                </li>
              ))}
              {t.hiddenJobCount > 0 ? (
                <li className="text-xs text-muted-foreground">
                  {T.list.hiddenJobs(t.hiddenJobCount)}
                </li>
              ) : null}
            </ul>
          )}
        </Fact>
        <Fact label={T.list.expiry}>
          {t.revokedAt ? (
            <span>{T.list.revoked(formatDateTime(t.revokedAt))}</span>
          ) : !t.expiresAt ? (
            <span className="flex flex-col gap-1">
              <SecurityBadge flag="never-expires" className="self-start" />
              <span className="text-xs text-muted-foreground">{T.list.neverExpiresHint}</span>
            </span>
          ) : (
            <span className="flex flex-col">
              <span>
                {state === "expired"
                  ? T.list.expired(formatDateTime(t.expiresAt))
                  : T.list.expires(formatDateTime(t.expiresAt))}
              </span>
              <span
                className={cn(
                  "text-xs",
                  state === "expiring" ? "text-status-partial-fg" : "text-muted-foreground",
                )}
              >
                {formatRelative(t.expiresAt, now)}
              </span>
            </span>
          )}
        </Fact>
        <Fact label={T.list.lastUse}>
          {t.lastSeenAt ? (
            <span className="flex flex-col gap-1">
              <span>{T.list.seen(formatRelative(t.lastSeenAt, now), t.lastSeenIp)}</span>
              {t.lastSeenRefused ? (
                <Tag tone="danger" compact title={T.list.refusedTitle}>
                  {T.list.refused}
                </Tag>
              ) : null}
            </span>
          ) : (
            <span className="text-muted-foreground">{T.list.never}</span>
          )}
        </Fact>
        <Fact label={T.list.addresses}>
          {t.allowAnyAddress ? (
            <SecurityBadge flag="any-address" />
          ) : (
            <ul className="flex flex-col font-mono text-xs">
              {t.allowedCidrs.map((c) => (
                <li key={c}>{c}</li>
              ))}
            </ul>
          )}
        </Fact>
      </dl>
      <p className="text-xs text-muted-foreground">
        {T.list.created(formatDateTime(t.createdAt), creator)}
      </p>
    </li>
  );
}
