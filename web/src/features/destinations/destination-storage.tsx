import { useQuery } from "@apollo/client/react";
import { RefreshCw, TriangleAlert } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { LedgerSection } from "@/features/connections/ui";
import type { DestinationSpaceQuery as SpaceResult } from "@/graphql/gen/graphql";
import { formatBytes, formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

import { DestinationSpaceQuery } from "./operations";
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

function SpaceView({
  space,
  stored,
  onRetry,
}: {
  space: Space;
  stored: number;
  onRetry: () => void;
}) {
  const storedText = formatBytes(stored);
  if (space.status !== "AVAILABLE") {
    return (
      <div className="flex flex-col gap-1" data-testid="space-reason">
        <p className="text-sm font-medium">
          {space.status === "UNSUPPORTED" ? ST.unsupported : ST.unavailable}
        </p>
        {space.reason ? <p className="text-sm text-muted-foreground">{space.reason}</p> : null}
        {space.status === "UNAVAILABLE" ? (
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="mt-1 self-start"
            onClick={onRetry}
          >
            <RefreshCw aria-hidden="true" /> {ST.checkAgain}
          </Button>
        ) : null}
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
      {space.checkedAt ? (
        <p className="text-xs text-muted-foreground">
          {ST.checked(formatRelative(space.checkedAt))}
        </p>
      ) : null}
    </div>
  );
}

/**
 * The detail page's storage section (staff): bekup's stored files from
 * destinationUsage, and the space the backend reports, fetched on demand
 * (destinationSpace; cached by the server).
 */
export function DestinationStorageSection({ id }: { id: string }) {
  const stored = useStoredByDestination();
  const row = stored.byId.get(id);
  const space = useQuery(DestinationSpaceQuery, { variables: { id }, fetchPolicy: "cache-first" });
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
      {space.data ? (
        <SpaceView
          space={space.data.destinationSpace}
          stored={storedBytes}
          onRetry={() => {
            void space.refetch();
          }}
        />
      ) : space.loading ? (
        <div className="flex flex-col gap-2">
          <span className="sr-only">{ST.checking}</span>
          <Skeleton className="h-4 w-72" />
          <Skeleton className="h-2 w-full max-w-xl" />
        </div>
      ) : space.error ? (
        <p className="text-sm text-muted-foreground" data-testid="space-reason">
          {ST.unavailable}
        </p>
      ) : null}
    </LedgerSection>
  );
}
