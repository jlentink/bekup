import { useApolloClient, useMutation } from "@apollo/client/react";
import { FileQuestion, Loader2, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { Tag } from "@/components/status/tag";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { reportActionError, useStepUp } from "@/features/auth/use-step-up";
import { Fact, Facts, LedgerSection } from "@/features/connections/ui";
import type { DestinationUnrecordedFilesQuery as FilesResult } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { formatBytes, formatDateTime, formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

import { DeleteUnrecordedFileMutation, DestinationUnrecordedFilesQuery } from "./operations";
import { UNRECORDED_PAGE_SIZE, uniqueById, useUnrecordedFiles } from "./use-unrecorded-files";

type Files = FilesResult["destinationUnrecordedFiles"];
type FileRow = Files["files"][number];

/** Id of the section heading target (the banner links to it). */
const SECTION_ID = "unrecorded-files";

const files = (n: number) => (n === 1 ? "1 unrecorded file" : `${String(n)} unrecorded files`);

const T = {
  title: "Unrecorded files",
  lead: "Files in bekup's layout ({job slug}/{timestamp}/{file}), older than 24 hours, that no backup record references: left over from an interrupted upload or cleanup, or put there by something else. A daily scan finds them. bekup never deletes them on its own.",
  banner: (n: number, size: string) => `${files(n)} (${size}) on this destination`,
  bannerLead:
    "No backup record references them, so retention never removes them. Review them and delete what you do not need.",
  bannerLink: "Review the files",
  none: "None",
  count: (n: number) => files(n),
  lastScan: "Last scan",
  neverScanned: "Not scanned yet: the daily scan has not finished on this destination.",
  totals: "Recorded",
  totalsValue: (n: number, size: string) => `${files(n)} · ${size}`,
  pruned: "Empty folders removed",
  prunedHint: "by the last scan",
  truncated:
    "The scan found more files than it records, or stopped at its object limit: this list is incomplete. Clean up and the next scan records the rest.",
  noDelete:
    "This destination is in no-delete mode: bekup cannot delete these files. Remove them with the storage's own tools.",
  empty: "No unrecorded files: every file in bekup's layout belongs to a backup record.",
  tableLabel: "Unrecorded files",
  cols: {
    key: "Key",
    size: "Size",
    modified: "Modified",
    firstSeen: "First seen",
    action: "Action",
  },
  unknown: "unknown",
  delete: "Delete",
  deleteLabel: (key: string) => `Delete ${key}`,
  noDeleteTitle: "Not possible in no-delete mode",
  loadMore: "Load more",
  loading: "Loading unrecorded files",
  shown: (shown: number, total: number) => `${String(shown)} of ${String(total)} shown`,
  confirmTitle: "Delete this unrecorded file?",
  confirmLead:
    "bekup deletes the file from the destination; this cannot be undone. Right before deleting it checks again that no backup record references the key and that the file has not changed in the last 24 hours.",
  confirmLabel: "Delete file",
  deleted: (key: string) => `Deleted ${key}.`,
  notDeleted: (key: string, message: string) => `${key} was not deleted: ${message}`,
} as const;

/**
 * The page-level warning on a destination with unrecorded files (D-209).
 * Admins only: the caller passes enabled = false for other roles.
 */
export function UnrecordedFilesBanner({
  destinationId,
  enabled,
}: {
  destinationId: string;
  enabled: boolean;
}) {
  const { data } = useUnrecordedFiles(destinationId, enabled);
  const u = data?.destinationUnrecordedFiles;
  if (!enabled || !u || u.totalCount === 0) return null;
  return (
    <Alert
      data-testid="unrecorded-banner"
      className="border-status-partial/45 bg-status-partial-soft text-status-partial-fg"
    >
      <FileQuestion aria-hidden="true" />
      <AlertTitle className="line-clamp-none">
        {T.banner(u.totalCount, formatBytes(u.totalBytes))}
      </AlertTitle>
      <AlertDescription className="text-foreground/80">
        <p>
          {T.bannerLead}{" "}
          <a href={`#${SECTION_ID}`} className="text-foreground underline underline-offset-4">
            {T.bannerLink}
          </a>
        </p>
      </AlertDescription>
    </Alert>
  );
}

/**
 * The admin section listing a destination's unrecorded files with a
 * per-file delete (step-up, confirm dialog; refused in no-delete mode).
 */
export function UnrecordedFilesSection({
  destinationId,
  noDelete,
}: {
  destinationId: string;
  noDelete: boolean;
}) {
  const client = useApolloClient();
  const { withStepUp } = useStepUp();
  const first = useUnrecordedFiles(destinationId, true);
  // Pages after the first, loaded with "Load more" (kept in component state).
  const [more, setMore] = useState<{ files: FileRow[]; next: string | null } | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const [target, setTarget] = useState<FileRow | null>(null);
  const [outcome, setOutcome] = useState<{ ok: boolean; text: string } | null>(null);
  const [remove] = useMutation(DeleteUnrecordedFileMutation, {
    // A separate request after the mutation (it must stay single-root).
    refetchQueries: [DestinationUnrecordedFilesQuery],
  });

  const u = first.data?.destinationUnrecordedFiles;
  // After a delete the first page is fetched again and may now start with
  // files already loaded by "Load more": each file is shown once.
  const rows = u ? uniqueById([...u.files, ...(more?.files ?? [])]) : [];
  const next = more ? more.next : (u?.nextCursor ?? null);

  const loadMore = async () => {
    if (!next) return;
    setLoadingMore(true);
    try {
      const r = await client.query({
        query: DestinationUnrecordedFilesQuery,
        variables: { destinationId, first: UNRECORDED_PAGE_SIZE, after: next },
        fetchPolicy: "network-only",
      });
      const page = r.data?.destinationUnrecordedFiles;
      if (page) {
        setMore((m) => ({
          files: [...(m?.files ?? []), ...page.files],
          next: page.nextCursor ?? null,
        }));
      }
    } catch (err) {
      reportActionError(err);
    } finally {
      setLoadingMore(false);
    }
  };

  const onDelete = async (f: FileRow) => {
    setOutcome(null);
    try {
      const r = await withStepUp(() => remove({ variables: { destinationId, id: f.id } }));
      const res = r.data?.deleteUnrecordedFile;
      if (!res) return;
      if (res.ok) {
        setMore((m) => (m ? { ...m, files: m.files.filter((x) => x.id !== f.id) } : m));
        setOutcome({ ok: true, text: T.deleted(f.key) });
        toast.success(T.deleted(f.key));
      } else {
        setOutcome({ ok: false, text: T.notDeleted(f.key, res.message) });
      }
    } catch (err) {
      const shown = reportActionError(err);
      if (shown) setOutcome({ ok: false, text: T.notDeleted(f.key, shown) });
    }
  };

  let body;
  if (!u && first.loading) {
    body = (
      <div role="status" aria-label={T.loading} className="flex flex-col gap-2">
        <Skeleton className="h-4 w-64" />
        <Skeleton className="h-4 w-full" />
        <Skeleton className="h-4 w-2/3" />
      </div>
    );
  } else if (!u) {
    body = (
      <Alert variant="destructive">
        <AlertDescription>{first.error ? userMessage(first.error) : T.loading}</AlertDescription>
      </Alert>
    );
  } else {
    body = (
      <>
        <Facts>
          <Fact term={T.lastScan}>
            {u.scannedAt ? (
              <span>
                {formatDateTime(u.scannedAt)}{" "}
                <span className="text-xs text-muted-foreground">
                  ({formatRelative(u.scannedAt)})
                </span>
              </span>
            ) : (
              <span className="text-muted-foreground">{T.neverScanned}</span>
            )}
          </Fact>
          <Fact term={T.totals}>
            <span data-testid="unrecorded-totals">
              {T.totalsValue(u.totalCount, formatBytes(u.totalBytes))}
            </span>
          </Fact>
          <Fact term={T.pruned}>
            {u.prunedDirectories}{" "}
            <span className="text-xs text-muted-foreground">{T.prunedHint}</span>
          </Fact>
        </Facts>
        {u.truncated ? (
          <Alert className="border-status-partial/45 bg-status-partial-soft text-status-partial-fg">
            <AlertDescription className="text-status-partial-fg">{T.truncated}</AlertDescription>
          </Alert>
        ) : null}
        {noDelete && rows.length > 0 ? (
          <p className="text-sm text-muted-foreground">{T.noDelete}</p>
        ) : null}
        {outcome ? (
          <p
            role={outcome.ok ? "status" : "alert"}
            className={cn(
              "text-sm break-all",
              outcome.ok ? "text-status-success-fg" : "text-status-failed-fg",
            )}
          >
            {outcome.text}
          </p>
        ) : null}
        {rows.length === 0 ? (
          <p className="text-sm text-muted-foreground">{T.empty}</p>
        ) : (
          <div className="rounded-lg border bg-card">
            <Table aria-label={T.tableLabel}>
              <TableHeader>
                <TableRow>
                  <TableHead>{T.cols.key}</TableHead>
                  <TableHead className="text-right">{T.cols.size}</TableHead>
                  <TableHead>{T.cols.modified}</TableHead>
                  <TableHead>{T.cols.firstSeen}</TableHead>
                  <TableHead className="text-right">
                    <span className="sr-only">{T.cols.action}</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((f) => (
                  <TableRow key={f.id} data-testid="unrecorded-file">
                    <TableCell className="max-w-[28rem] font-mono text-xs break-all whitespace-normal">
                      {f.key}
                    </TableCell>
                    <TableCell className="text-right font-mono text-xs whitespace-nowrap">
                      {formatBytes(f.size)}
                    </TableCell>
                    <TableCell className="text-xs whitespace-nowrap text-muted-foreground">
                      {f.modifiedAt ? formatDateTime(f.modifiedAt) : T.unknown}
                    </TableCell>
                    <TableCell className="text-xs whitespace-nowrap text-muted-foreground">
                      {formatDateTime(f.firstSeenAt)}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        type="button"
                        size="sm"
                        variant="ghost"
                        className="text-destructive hover:bg-status-failed-soft hover:text-destructive"
                        disabled={noDelete}
                        aria-label={T.deleteLabel(f.key)}
                        title={noDelete ? T.noDeleteTitle : undefined}
                        onClick={() => {
                          setTarget(f);
                        }}
                      >
                        <Trash2 aria-hidden="true" /> {T.delete}
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
        {rows.length > 0 ? (
          <div className="flex flex-wrap items-center gap-3">
            <span className="font-mono text-xs text-muted-foreground">
              {T.shown(rows.length, u.totalCount)}
            </span>
            {next ? (
              <Button
                type="button"
                size="sm"
                variant="outline"
                disabled={loadingMore}
                onClick={() => void loadMore()}
              >
                {loadingMore ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
                {T.loadMore}
              </Button>
            ) : null}
          </div>
        ) : null}
      </>
    );
  }

  return (
    <div id={SECTION_ID} className="scroll-mt-20 border-b border-rule last:border-b-0">
      <LedgerSection
        title={T.title}
        lead={T.lead}
        aside={
          u ? (
            u.totalCount > 0 ? (
              <Tag tone="warning">{T.count(u.totalCount)}</Tag>
            ) : (
              <Tag tone="success">{T.none}</Tag>
            )
          ) : null
        }
      >
        {body}
      </LedgerSection>
      <ConfirmDialog
        open={target !== null}
        onOpenChange={(o) => {
          if (!o) setTarget(null);
        }}
        title={T.confirmTitle}
        description={
          <>
            <span className="mb-2 block font-mono text-xs break-all text-foreground">
              {target?.key}
            </span>
            {T.confirmLead}
          </>
        }
        confirmLabel={T.confirmLabel}
        destructive
        onConfirm={async () => {
          if (target) await onDelete(target);
        }}
      />
    </div>
  );
}
