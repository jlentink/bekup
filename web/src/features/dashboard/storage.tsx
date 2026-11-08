import { HardDrive } from "lucide-react";
import { Link } from "react-router";

import { formatBytes } from "@/lib/format";
import { defineMessages, plural } from "@/lib/i18n";

import type { StorageRow } from "./model";
import { Tile, TileEmpty, TileError, TileSkeleton } from "./tile";

const T = defineMessages({
  en: {
    title: "Storage",
    link: "Destinations",
    loading: "Loading storage",
    empty: "No destinations yet.",
    listLabel: "Stored bytes per destination",
    objects: (n: number) => plural(n, { one: "1 object", other: `${String(n)} objects` }),
    type: { DISK: "Disk", S3: "S3", SFTP: "SFTP", WEBDAV: "WebDAV" },
  },
  nl: {
    title: "Opslag",
    link: "Bestemmingen",
    loading: "Opslag laden",
    empty: "Nog geen bestemmingen.",
    listLabel: "Opgeslagen bytes per bestemming",
    objects: (n: number) => plural(n, { one: "1 object", other: `${String(n)} objecten` }),
    type: { DISK: "Schijf", S3: "S3", SFTP: "SFTP", WEBDAV: "WebDAV" },
  },
});

const TYPE_LABEL = T.type;

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
    <Tile folio="06" title={T.title} to="/destinations" linkLabel={T.link} testId="tile-storage">
      {loading && rows.length === 0 ? (
        <TileSkeleton rows={3} label={T.loading} />
      ) : error && rows.length === 0 ? (
        <TileError error={error} />
      ) : rows.length === 0 ? (
        <TileEmpty icon={<HardDrive aria-hidden="true" className="size-4" />}>{T.empty}</TileEmpty>
      ) : (
        <ul aria-label={T.listLabel} className="flex flex-col gap-3">
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
              <span className="text-2xs text-muted-foreground">{T.objects(r.objects)}</span>
            </li>
          ))}
        </ul>
      )}
    </Tile>
  );
}
