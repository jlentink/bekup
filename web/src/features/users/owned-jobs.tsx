import { skipToken, useMutation, useQuery } from "@apollo/client/react";
import { DatabaseBackup, Info, Loader2, Plus, Search, X } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { Link } from "react-router";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Section } from "@/features/account/section";
import { reportActionError, useStepUp } from "@/features/auth/use-step-up";
import type { AdminOwnedJobsQuery, Role } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { formatDateTime, formatRelative } from "@/lib/format";
import { messages } from "@/lib/messages";
import { AssignJobOwnersMutation, RemoveJobOwnerMutation } from "@/lib/operations";
import { cn } from "@/lib/utils";

import { usersMessages as M } from "./messages";
import { AssignableJobsQuery, OwnedJobsQuery } from "./operations";
import { ownershipBlock } from "./status";

type JobNode = AdminOwnedJobsQuery["jobs"]["edges"][number]["node"];

/** Owned jobs per page ("Show more" loads the next). */
const JOB_PAGE = 50;
/** Candidates per search in the assign dialog. */
const ASSIGN_PAGE = 50;

export interface OwnedJobsSubject {
  id: string;
  displayName: string;
  role: Role;
  disabled: boolean;
  deletedAt: string | null;
}

/** Jobs an account owns, with assign/remove (step-up; D-067). */
export function OwnedJobsSection({ subject }: { subject: OwnedJobsSubject }) {
  const { withStepUp } = useStepUp();
  // The server filters by owner (D-184): no client-side scan of all jobs.
  const { data, error, refetch, fetchMore } = useQuery(OwnedJobsQuery, {
    variables: { ownerId: subject.id, first: JOB_PAGE },
    fetchPolicy: "cache-and-network",
  });
  const [loadingMore, setLoadingMore] = useState(false);
  const [remove] = useMutation(RemoveJobOwnerMutation);
  const [assigning, setAssigning] = useState(false);
  const [removing, setRemoving] = useState<JobNode | null>(null);

  const owned = (data?.jobs.edges ?? []).map((e) => e.node);
  const ownedIds = new Set(owned.map((j) => j.id));
  const pageInfo = data?.jobs.pageInfo;
  const loadMore = async () => {
    if (!pageInfo?.endCursor) return;
    setLoadingMore(true);
    try {
      await fetchMore({
        variables: { after: pageInfo.endCursor },
        updateQuery: (prev, { fetchMoreResult }) => ({
          jobs: {
            ...fetchMoreResult.jobs,
            edges: [...prev.jobs.edges, ...fetchMoreResult.jobs.edges],
          },
        }),
      });
    } catch (err) {
      toast.error(userMessage(err));
    } finally {
      setLoadingMore(false);
    }
  };
  const block = ownershipBlock(subject);
  const addedAt = (j: JobNode) => j.owners?.find((o) => o.userId === subject.id)?.addedAt;

  return (
    <Section
      icon={DatabaseBackup}
      title={M.jobsHeading}
      lead={M.jobsLead}
      status={
        data ? (
          <span className="font-mono text-2xs text-muted-foreground">
            {owned.length}
            {pageInfo?.hasNextPage ? "+" : ""}
          </span>
        ) : null
      }
    >
      {block === "role" ? (
        <p className="flex gap-2 rounded-md border border-rule bg-surface-sunken px-3 py-2.5 text-sm text-muted-foreground">
          <Info aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
          {M.ownerRoleOnly}
        </p>
      ) : null}
      {error && !data ? (
        <Alert variant="destructive">
          <AlertDescription className="flex flex-wrap items-center gap-3">
            {userMessage(error)}
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                void refetch().catch(() => undefined);
              }}
            >
              {messages.common.retry}
            </Button>
          </AlertDescription>
        </Alert>
      ) : !data ? (
        <Skeleton className="h-12 w-full" />
      ) : owned.length === 0 ? (
        <p className="text-sm text-muted-foreground">{M.noJobs}</p>
      ) : (
        <ul
          aria-label={M.jobsHeading}
          className="divide-y divide-rule rounded-md border bg-surface px-4 py-1"
        >
          {owned.map((j) => (
            <li key={j.id} className="flex items-center gap-3 py-2.5">
              <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                <Link
                  to={`/jobs/${j.id}`}
                  className="truncate text-sm font-medium underline-offset-4 hover:underline"
                >
                  {j.name}
                </Link>
                <span className="flex flex-wrap gap-x-3 font-mono text-2xs text-muted-foreground">
                  <span>{j.slug}</span>
                  {!j.enabled ? <span>{M.jobDisabled}</span> : null}
                  <span title={formatDateTime(addedAt(j))}>{formatRelative(addedAt(j))}</span>
                </span>
              </div>
              {!subject.deletedAt ? (
                <Button
                  variant="ghost"
                  size="sm"
                  aria-label={M.removeLabel(j.name)}
                  onClick={() => {
                    setRemoving(j);
                  }}
                >
                  <X aria-hidden="true" /> {M.remove}
                </Button>
              ) : null}
            </li>
          ))}
        </ul>
      )}
      {pageInfo?.hasNextPage ? (
        <Button
          variant="ghost"
          size="sm"
          className="self-start"
          disabled={loadingMore}
          onClick={() => {
            void loadMore();
          }}
        >
          {loadingMore ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
          {M.loadMoreJobs}
        </Button>
      ) : null}
      {block === null ? (
        <Button
          variant="outline"
          size="sm"
          className="self-start"
          disabled={!data}
          onClick={() => {
            setAssigning(true);
          }}
        >
          <Plus aria-hidden="true" /> {M.assign}
        </Button>
      ) : null}

      <AssignJobsDialog
        open={assigning}
        onOpenChange={setAssigning}
        subject={subject}
        ownedIds={ownedIds}
        onAssigned={() => {
          void refetch().catch(() => undefined);
        }}
      />
      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(o) => {
          if (!o) setRemoving(null);
        }}
        title={M.removeTitle}
        description={removing ? M.removeLead(removing.name) : ""}
        confirmLabel={M.remove}
        destructive
        onConfirm={async () => {
          if (!removing) return;
          try {
            await withStepUp(() =>
              remove({ variables: { jobId: removing.id, userId: subject.id } }),
            );
          } catch (err) {
            reportActionError(err);
            throw err;
          }
          toast.success(M.removed);
          await refetch().catch(() => undefined);
        }}
      />
    </Section>
  );
}

function AssignJobsDialog({
  open,
  onOpenChange,
  subject,
  ownedIds,
  onAssigned,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  subject: OwnedJobsSubject;
  /** Jobs the account already owns (loaded so far); never offered again. */
  ownedIds: ReadonlySet<string>;
  onAssigned: () => void;
}) {
  const { withStepUp } = useStepUp();
  const [assign] = useMutation(AssignJobOwnersMutation);
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState(false);
  const searchId = useId();
  const [term, setTerm] = useState("");
  useEffect(() => {
    const t = setTimeout(() => {
      setTerm(query.trim());
    }, 250);
    return () => {
      clearTimeout(t);
    };
  }, [query]);
  // Searched on the server, only while the dialog is open.
  const candidates = useQuery(
    AssignableJobsQuery,
    open ? { variables: { search: term || null, first: ASSIGN_PAGE } } : skipToken,
  );
  const shown = candidates.data ?? candidates.previousData;
  const visible = (shown?.jobs.edges ?? []).map((e) => e.node).filter((j) => !ownedIds.has(j.id));

  const close = (o: boolean) => {
    if (busy) return;
    if (!o) {
      setSelected(new Set());
      setQuery("");
    }
    onOpenChange(o);
  };

  const submit = async () => {
    setBusy(true);
    const pending = new Set(selected);
    let done = 0;
    try {
      // One step-up covers the batch; a retry continues where it stopped.
      await withStepUp(async () => {
        for (const jobId of [...pending]) {
          await assign({ variables: { jobId, users: [subject.id] } });
          pending.delete(jobId);
          done++;
        }
      });
      toast.success(M.assigned(done));
      setBusy(false);
      close(false);
    } catch (err) {
      reportActionError(err);
      setSelected(pending);
      setBusy(false);
    } finally {
      if (done > 0) onAssigned();
    }
  };

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className="gap-5 sm:max-w-lg">
        <DialogHeader>
          <DialogTitle className="font-display text-2xl font-normal">{M.assignTitle}</DialogTitle>
          <DialogDescription>{M.assignLead(subject.displayName)}</DialogDescription>
        </DialogHeader>
        <div className="relative">
          <label htmlFor={searchId} className="sr-only">
            {M.assignSearch}
          </label>
          <Search
            aria-hidden="true"
            className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground"
          />
          <Input
            id={searchId}
            type="search"
            className="pl-8"
            placeholder={M.assignSearch}
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
            }}
          />
        </div>
        {candidates.error && !shown ? (
          <Alert variant="destructive">
            <AlertDescription>{userMessage(candidates.error)}</AlertDescription>
          </Alert>
        ) : !shown ? (
          <Skeleton className="h-24 w-full" />
        ) : visible.length === 0 ? (
          <p className="py-6 text-center text-sm text-muted-foreground">{M.assignNone}</p>
        ) : (
          <ul
            aria-label={M.assignTitle}
            className="max-h-72 divide-y divide-rule overflow-y-auto rounded-md border bg-surface"
          >
            {visible.map((j) => {
              const checked = selected.has(j.id);
              return (
                <li key={j.id}>
                  <label
                    className={cn(
                      "flex cursor-pointer items-center gap-3 px-3 py-2 hover:bg-accent/40",
                      checked && "bg-accent/60",
                    )}
                  >
                    <input
                      type="checkbox"
                      checked={checked}
                      onChange={(e) => {
                        const next = new Set(selected);
                        if (e.target.checked) next.add(j.id);
                        else next.delete(j.id);
                        setSelected(next);
                      }}
                      className="size-4 accent-primary"
                    />
                    <span className="min-w-0 truncate text-sm font-medium">{j.name}</span>
                    <span className="ml-auto font-mono text-2xs text-muted-foreground">
                      {j.slug}
                      {!j.enabled ? ` · ${M.jobDisabled}` : ""}
                    </span>
                  </label>
                </li>
              );
            })}
          </ul>
        )}
        {shown?.jobs.pageInfo.hasNextPage ? (
          <p className="text-xs text-muted-foreground">{M.assignTruncated}</p>
        ) : null}
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" disabled={busy}>
              {messages.common.cancel}
            </Button>
          </DialogClose>
          <Button
            disabled={busy || selected.size === 0}
            onClick={() => {
              void submit();
            }}
          >
            {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
            {M.assignSubmit(selected.size)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
