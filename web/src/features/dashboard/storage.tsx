import { HardDrive } from "lucide-react";
import { Link } from "react-router";

import { formatBytes } from "@/lib/format";

import type { StorageRow } from "./model";
import { Tile, TileEmpty, TileError, TileSkeleton } from "./tile";

const TYPE_LABEL = { DISK: "Disk", S3: "S3", SFTP: "SFTP" } as const;

/**
 * Bytes of present artifacts per destination (destinationUsage, an exact
 * server aggregate): one series, so one hue and no legend; values are
 * printed beside each bar (the table view).
 */
export function StorageTile({
  rows,
  loading,
  error,
}: {
  rows: StorageRow[];
  loading: boolean;
  error: unknown;
}) {
  return (
    <Tile
      folio="06"
      title="Storage"
      to="/destinations"
      linkLabel="Destinations"
      testId="tile-storage"
    >
      {loading && rows.length === 0 ? (
        <TileSkeleton rows={3} label="Loading storage" />
      ) : error && rows.length === 0 ? (
        <TileError error={error} />
      ) : rows.length === 0 ? (
        <TileEmpty icon={<HardDrive aria-hidden="true" className="size-4" />}>
          No destinations yet.
        </TileEmpty>
      ) : (
        <ul aria-label="Stored bytes per destination" className="flex flex-col gap-3">
          {rows.map((r) => (
            <li key={r.id} data-testid="storage-row" className="flex flex-col gap-1">
              <span className="flex items-baseline gap-2">
                <Link
                  to={`/destinations/${r.id}`}
                  className="min-w-0 truncate font-medium underline-offset-4 hover:underline"
                >
                  {r.name}
                </Link>
                <span className="font-mono text-2xs text-muted-foreground">
                  {TYPE_LABEL[r.type]}
                </span>
                <span className="ml-auto font-mono text-xs" data-testid="storage-bytes">
                  {formatBytes(r.bytes)}
                </span>
              </span>
              <svg
                viewBox="0 0 100 4"
                preserveAspectRatio="none"
                className="h-1.5 w-full"
                aria-hidden="true"
              >
                <rect x={0} y={0} width={100} height={4} className="fill-surface-sunken" />
                {r.share > 0 ? (
                  <rect
                    x={0}
                    y={0}
                    width={Math.max(0.8, r.share * 100)}
                    height={4}
                    className="fill-primary"
                  />
                ) : null}
              </svg>
              <span className="text-2xs text-muted-foreground">
                {r.objects === 1 ? "1 object" : `${String(r.objects)} objects`}
              </span>
            </li>
          ))}
        </ul>
      )}
    </Tile>
  );
}
