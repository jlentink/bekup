import { skipToken, useQuery } from "@apollo/client/react";
import { useState } from "react";
import { Clock, RefreshCw, TriangleAlert } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { LedgerSection } from "@/features/connections/ui";
import type { DestinationSpaceQuery as SpaceResult } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { formatBytes, formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

import { DestinationSpaceCheckQuery, DestinationSpaceQuery } from "./operations";
import { ST } from "./storage-text";
import { type StoredRow, useStoredByDestination } from "./use-stored";

type Space = SpaceResult["destinationSpace"];

/** The list's "Stored" cell: size and number of backups. */
export function StoredCell({ row, loaded }: { row: StoredRow | undefined; loaded: boolean }) {
  if (!loaded) return <Skeleton className="ml-auto h-4 w-16" />;
  if (!row || row.artifactCount === 0) {
    return <span className="text-xs text-muted-foreground">{ST.none}</span>;
  }
  return (
    <span className="flex flex-col items-end gap-0.5" data-testid="destination-stored">
      <span className="font-mono text-xs">{formatBytes(row.storedBytes)}</span>
      <span className="text-2xs text-muted-foreground">{ST.backups(row.backupCount)}</span>
    </span>
  );
}

/**
 * Space bar (the dashboard storage tile's visual): bekup's backups, other
 * files, free; SVG geometry only (no inline styles, CSP). Low space colors
 * the used part and is always spelled out next to it.
 */
function SpaceBar({
  total,
  free,
  stored,
  low,
}: {
  total: number;
  free: number;
  stored: number;
  low: boolean;
}) {
  const used = Math.max(0, total - free);
  const mine = Math.min(stored, used);
  const others = used - mine;
  const pct = (v: number) => (total > 0 ? (v / total) * 100 : 0);
  const minePct = mine > 0 ? Math.max(0.8, pct(mine)) : 0;
  const othersPct = Math.min(100 - minePct, pct(others));
  return (
    <div className="flex flex-col gap-2">
      <svg
        role="img"
        aria-label={ST.barLabel(
          formatBytes(mine),
          formatBytes(others),
          formatBytes(free),
          formatBytes(total),
        )}
        viewBox="0 0 100 4"
        preserveAspectRatio="none"
        className="h-2 w-full max-w-xl"
        data-testid="space-bar"
      >
        <rect x={0} y={0} width={100} height={4} className="fill-surface-sunken" />
        {othersPct > 0 ? (
          <rect
            x={minePct}
            y={0}
            width={othersPct}
            height={4}
            className={low ? "fill-status-partial" : "fill-muted-foreground/60"}
          />
        ) : null}
        {minePct > 0 ? (
          <rect x={0} y={0} width={minePct} height={4} className="fill-primary" />
        ) : null}
      </svg>
      <ul
        className="flex flex-wrap gap-x-4 gap-y-1 text-2xs text-muted-foreground"
        aria-hidden="true"
      >
        <li className="inline-flex items-center gap-1.5">
          <span className="size-2 rounded-xs bg-primary" /> {ST.legendBekup}
        </li>
        <li className="inline-flex items-center gap-1.5">
          <span
            className={cn(
              "size-2 rounded-xs",
              low ? "bg-status-partial" : "bg-muted-foreground/60",
            )}
          />{" "}
          {ST.legendOther}
        </li>
        <li className="inline-flex items-center gap-1.5">
          <span className="size-2 rounded-xs border bg-surface-sunken" /> {ST.legendFree}
        </li>
      </ul>
    </div>
  );
}

/**
 * When the space was checked, a stale warning, a failed "Check now" (shown
 * next to the stored answer it did not replace) and the "Check now" action.
 */
function CheckedFooter({
  space,
  stored,
  checking,
  liveProblem,
  onCheck,
}: {
  space: Space;
  stored: boolean;
  checking: boolean;
  liveProblem: string | null;
  onCheck: () => void;
}) {
  const when = space.checkedAt ? formatRelative(space.checkedAt) : null;
  return (
    <div className="flex flex-col gap-2">
      {liveProblem && !checking ? (
        <div
          role="alert"
          data-testid="space-live-failed"
          className="flex items-start gap-2 rounded-md border border-status-partial/40 bg-status-partial-soft px-3 py-2 text-sm text-status-partial-fg"
        >
          <TriangleAlert aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
          <span>{ST.liveFailed(liveProblem)}</span>
        </div>
      ) : null}
      {stored && space.stale && when ? (
        <div
          role="status"
          data-testid="space-stale"
          className="flex items-start gap-2 rounded-md border border-status-partial/40 bg-status-partial-soft px-3 py-2 text-sm text-status-partial-fg"
        >
          <Clock aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
          <span>{ST.stale(when)}</span>
        </div>
      ) : null}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        {when && !(stored && space.stale) ? (
          <p className="text-xs text-muted-foreground" data-testid="space-checked">
            {stored ? ST.checkedStored(when) : ST.checked(when)}
          </p>
        ) : null}
        {space.status !== "UNSUPPORTED" ? (
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={checking}
            aria-busy={checking}
            onClick={onCheck}
          >
            <RefreshCw aria-hidden="true" className={cn(checking && "animate-spin")} />{" "}
            {space.status === "UNAVAILABLE" ? ST.checkAgain : ST.checkNow}
          </Button>
        ) : null}
      </div>
    </div>
  );
}

function SpaceView({
  space,
  stored,
  fromStore,
  checking,
  liveProblem,
  onCheck,
}: {
  space: Space;
  stored: number;
  fromStore: boolean;
  checking: boolean;
  liveProblem: string | null;
  onCheck: () => void;
}) {
  const storedText = formatBytes(stored);
  const footer = (
    <CheckedFooter
      space={space}
      stored={fromStore}
      checking={checking}
      liveProblem={liveProblem}
      onCheck={onCheck}
    />
  );
  if (space.status !== "AVAILABLE") {
    return (
      <div className="flex flex-col gap-2">
        <div className="flex flex-col gap-1" data-testid="space-reason">
          <p className="text-sm font-medium">
            {space.status === "UNSUPPORTED" ? ST.unsupported : ST.unavailable}
          </p>
          {space.reason ? <p className="text-sm text-muted-foreground">{space.reason}</p> : null}
        </div>
        {footer}
      </div>
    );
  }
  const { totalBytes: total, freeBytes: free, usedBytes: used } = space;
  return (
    <div className="flex flex-col gap-3">
      <p className="text-sm" data-testid="space-line">
        {total != null && free != null
          ? ST.spaceLine(storedText, formatBytes(free), formatBytes(total))
          : free != null
            ? ST.spaceFreeOnly(storedText, formatBytes(free))
            : ST.spaceUsedOnly(storedText, formatBytes(used ?? 0))}
      </p>
      {total != null && free != null ? (
        <SpaceBar total={total} free={free} stored={stored} low={space.low} />
      ) : null}
      {space.low ? (
        <div
          role="status"
          data-testid="space-low"
          className="flex items-start gap-2 rounded-md border border-status-partial/40 bg-status-partial-soft px-3 py-2 text-sm text-status-partial-fg"
        >
          <TriangleAlert aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
          <span>
            <span className="font-medium">{ST.low}.</span>{" "}
            {ST.lowLead(formatBytes(space.newestSetBytes))}
          </span>
        </div>
      ) : null}
      {footer}
    </div>
  );
}

/**
 * The detail page's storage section (staff): bekup's stored files from
 * destinationUsage, and the space the backend reports: the stored answer
 * of the hourly space check by default (Destination.spaceCheck, D-227;
 * never asks the backend), the live destinationSpace answer (cached by the
 * server) on "Check now" or when nothing is stored for the current
 * settings. A "Check now" that fails (an API error such as a rate limit, or
 * an unavailable answer) never hides a stored answer: the stored one stays
 * with the failure next to it (D-227 amendment 1).
 */
export function DestinationStorageSection({ id }: { id: string }) {
  const stored = useStoredByDestination();
  const row = stored.byId.get(id);
  const check = useQuery(DestinationSpaceCheckQuery, {
    variables: { id },
    fetchPolicy: "cache-and-network",
  });
  const [asked, setAsked] = useState(false);
  const storedSpace = check.data?.destination?.spaceCheck ?? null;
  const wantLive = asked || (check.data !== undefined && storedSpace === null) || !!check.error;
  const live = useQuery(
    DestinationSpaceQuery,
    wantLive
      ? { variables: { id }, fetchPolicy: "network-only", notifyOnNetworkStatusChange: true }
      : skipToken,
  );
  const checking = wantLive && live.loading;
  const liveAnswer = wantLive ? (live.data?.destinationSpace ?? null) : null;
  // A failed live check next to a stored answer: keep the stored answer
  // (unless it is a failure itself, which the fresh failure replaces).
  let liveProblem: string | null = null;
  if (storedSpace && wantLive && !checking) {
    if (live.error) {
      liveProblem = userMessage(live.error);
    } else if (liveAnswer?.status === "UNAVAILABLE" && storedSpace.status !== "UNAVAILABLE") {
      liveProblem = liveAnswer.reason ?? ST.unavailable;
    }
  }
  const liveSpace = liveProblem !== null ? null : liveAnswer;
  const shown = liveSpace ?? storedSpace;
  const onCheck = () => {
    if (wantLive) {
      void live.refetch();
    } else {
      setAsked(true);
    }
  };
  const storedBytes = row?.storedBytes ?? 0;
  return (
    <LedgerSection title={ST.section} lead={ST.lead}>
      <p className="text-sm" data-testid="stored-line">
        {stored.failed
          ? ST.storedUnknown
          : stored.loaded
            ? ST.storedLine(
                formatBytes(storedBytes),
                ST.backups(row?.backupCount ?? 0),
                ST.files(row?.artifactCount ?? 0),
              )
            : null}
      </p>
      {shown ? (
        <SpaceView
          space={shown}
          stored={storedBytes}
          fromStore={liveSpace === null}
          checking={checking}
          liveProblem={liveProblem}
          onCheck={onCheck}
        />
      ) : check.loading || checking ? (
        <div className="flex flex-col gap-2">
          <span className="sr-only">{ST.checking}</span>
          <Skeleton className="h-4 w-72" />
          <Skeleton className="h-2 w-full max-w-xl" />
        </div>
      ) : live.error ? (
        <p className="text-sm text-muted-foreground" data-testid="space-reason">
          {ST.unavailable}
        </p>
      ) : null}
    </LedgerSection>
  );
}
