import { skipToken, useMutation, useQuery } from "@apollo/client/react";
import { ArrowDown, FastForward, Loader2, Play } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useNavigate } from "react-router";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { ApiCode, firstApiError, userMessage } from "@/lib/api-error";
import { formatDateTime, formatRelative } from "@/lib/format";

import {
  JobDetailQuery,
  NextOccurrencesQuery,
  RunJobNowMutation,
  StartNextRunEarlyMutation,
} from "./operations";
import { formatInZone, scheduleToInput, type Schedule } from "./schedule";

const T = {
  runNow: "Run now",
  runNowSub: "extra run",
  runNowTitle: "Start an extra run now?",
  runNowLead:
    "bekup queues one additional backup right away. The schedule does not change: the next planned run still happens at its time.",
  runNowNext: (at: string) => `Next planned run stays at ${at}.`,
  runNowQueued: "Extra run queued.",
  startEarly: "Start next planned run early",
  startEarlyTitle: "Start the next planned run now?",
  startEarlyLead:
    "The next planned occurrence runs now instead of at its time, and is then skipped. The schedule continues with the occurrence after it.",
  moves: "This occurrence moves",
  startsNow: "Starts now",
  resultingNext: "Next planned run afterwards",
  resultingUnknown: "The occurrence after it, as the schedule defines.",
  startEarlyQueued: (next: string | null) =>
    next ? `Started early. Next planned run: ${next}.` : "Started early.",
  confirm: "Start now",
  cancel: "Cancel",
  view: "View run",
  conflict: "A run of this job is already queued or running. Wait for it to finish.",
  noPlanned: "This job has no planned run to start early.",
  disabledHint: "The job is disabled.",
  oneAhead:
    "Only one occurrence can be pulled forward. You can start the following one early once the original time has passed.",
} as const;

interface JobLike {
  id: string;
  name: string;
  enabled: boolean;
  nextRunAt?: string | null;
  schedule: Schedule;
}

function useAfterRun(jobId: string) {
  const navigate = useNavigate();
  return (runId: string) => ({
    action: {
      label: T.view,
      onClick: () => {
        void navigate(`/runs/${runId}`);
      },
    },
    id: `run-${jobId}`,
  });
}

/**
 * "Run now (extra run)" and "Start next planned run early" (D-156, D-157,
 * D-160) for admins, maintainers and owners. The dialogs explain the
 * difference; the server's refusals are shown in place: CONFLICT (a run
 * is active; also a disabled job for runJobNow), VALIDATION (disabled, no
 * planned run, already started early) for startNextRunEarly.
 */
export function RunActions({ job, canPreview }: { job: JobLike; canPreview: boolean }) {
  const [dialog, setDialog] = useState<null | "now" | "early">(null);
  const close = () => {
    setDialog(null);
  };
  const hasPlanned = job.enabled && Boolean(job.nextRunAt) && job.schedule.kind !== "MANUAL";
  return (
    <>
      <Button
        onClick={() => {
          setDialog("now");
        }}
        title={job.enabled ? undefined : T.disabledHint}
      >
        <Play aria-hidden="true" /> {T.runNow}
        <span className="font-normal opacity-80">({T.runNowSub})</span>
      </Button>
      <Button
        variant="outline"
        disabled={!hasPlanned}
        title={!job.enabled ? T.disabledHint : !hasPlanned ? T.noPlanned : undefined}
        onClick={() => {
          setDialog("early");
        }}
      >
        <FastForward aria-hidden="true" /> {T.startEarly}
      </Button>
      {dialog === "now" ? <RunNowDialog job={job} onClose={close} /> : null}
      {dialog === "early" ? (
        <StartEarlyDialog job={job} canPreview={canPreview} onClose={close} />
      ) : null}
    </>
  );
}

/** Maps the two endpoints' refusals to one message (D-160 item 6). */
function runRefusal(err: unknown): string {
  const e = firstApiError(err);
  if (e?.code === ApiCode.conflict && /queued|running|active/i.test(e.message)) return T.conflict;
  if (e?.code === ApiCode.validation && /early/i.test(e.message)) return T.oneAhead;
  return userMessage(err);
}

function ActionFrame({
  title,
  lead,
  error,
  busy,
  confirmLabel,
  onConfirm,
  onClose,
  children,
}: {
  title: string;
  lead: string;
  error: string | null;
  busy: boolean;
  confirmLabel: string;
  onConfirm: () => void;
  onClose: () => void;
  children?: ReactNode;
}) {
  return (
    <Dialog
      open
      onOpenChange={(o) => {
        if (!o && !busy) onClose();
      }}
    >
      <DialogContent className="gap-5 sm:max-w-lg">
        <DialogHeader>
          <DialogTitle className="font-display text-2xl font-normal">{title}</DialogTitle>
          <DialogDescription>{lead}</DialogDescription>
        </DialogHeader>
        {children}
        {error ? (
          <p
            role="alert"
            className="rounded-md border border-destructive/40 bg-status-failed-soft px-3 py-2 text-sm text-status-failed-fg"
          >
            {error}
          </p>
        ) : null}
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            {T.cancel}
          </Button>
          <Button disabled={busy} onClick={onConfirm}>
            {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
            {confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function RunNowDialog({ job, onClose }: { job: JobLike; onClose: () => void }) {
  const [run, { loading }] = useMutation(RunJobNowMutation, { refetchQueries: [JobDetailQuery] });
  const [error, setError] = useState<string | null>(null);
  const toastFor = useAfterRun(job.id);
  return (
    <ActionFrame
      title={T.runNowTitle}
      lead={T.runNowLead}
      error={error}
      busy={loading}
      confirmLabel={T.confirm}
      onClose={onClose}
      onConfirm={() => {
        setError(null);
        run({ variables: { id: job.id } }).then(
          (r) => {
            const res = r.data?.runJobNow;
            if (res) toast.success(T.runNowQueued, toastFor(res.runId));
            onClose();
          },
          (err: unknown) => {
            setError(runRefusal(err));
          },
        );
      }}
    >
      {job.nextRunAt && job.enabled ? (
        <p className="text-sm text-muted-foreground">
          {T.runNowNext(formatDateTime(job.nextRunAt))}
        </p>
      ) : null}
    </ActionFrame>
  );
}

function StartEarlyDialog({
  job,
  canPreview,
  onClose,
}: {
  job: JobLike;
  canPreview: boolean;
  onClose: () => void;
}) {
  const [start, { loading }] = useMutation(StartNextRunEarlyMutation, {
    refetchQueries: [JobDetailQuery],
  });
  const [error, setError] = useState<string | null>(null);
  const toastFor = useAfterRun(job.id);
  const zone = job.schedule.timezone;
  const preview = useQuery(
    NextOccurrencesQuery,
    canPreview && job.nextRunAt
      ? { variables: { input: scheduleToInput(job.schedule) }, fetchPolicy: "network-only" }
      : skipToken,
  );
  const planned = job.nextRunAt ?? null;
  const plannedMs = planned ? Date.parse(planned) : NaN;
  const following =
    preview.data?.previewSchedule.next.find((iso) => Date.parse(iso) > plannedMs) ?? null;

  return (
    <ActionFrame
      title={T.startEarlyTitle}
      lead={T.startEarlyLead}
      error={error}
      busy={loading}
      confirmLabel={T.confirm}
      onClose={onClose}
      onConfirm={() => {
        setError(null);
        start({ variables: { id: job.id } }).then(
          (r) => {
            const res = r.data?.startNextRunEarly;
            if (res) {
              const next = res.job.nextRunAt ? formatInZone(res.job.nextRunAt, zone) : null;
              toast.success(T.startEarlyQueued(next), toastFor(res.runId));
            }
            onClose();
          },
          (err: unknown) => {
            setError(runRefusal(err));
          },
        );
      }}
    >
      {/* A ledger correction: the planned entry struck through, the amended one below. */}
      <ol
        className="flex flex-col gap-2 rounded-md border bg-surface-sunken px-4 py-3.5"
        data-testid="early-ledger"
      >
        <li className="flex flex-col gap-0.5">
          <span className="eyebrow">{T.moves}</span>
          {planned ? (
            <span className="flex flex-wrap items-baseline gap-x-3">
              <time
                dateTime={planned}
                className="font-mono text-sm text-muted-foreground line-through decoration-status-partial decoration-2"
              >
                {formatInZone(planned, zone)}
              </time>
              <span className="text-sm font-medium text-primary">{T.startsNow}</span>
            </span>
          ) : null}
        </li>
        <li aria-hidden="true" className="pl-1 text-muted-foreground">
          <ArrowDown className="size-3.5" />
        </li>
        <li className="flex flex-col gap-0.5">
          <span className="eyebrow">{T.resultingNext}</span>
          {following ? (
            <span className="flex flex-wrap items-baseline gap-x-3">
              <time
                dateTime={following}
                className="font-mono text-sm"
                data-testid="early-following"
              >
                {formatInZone(following, zone)}
              </time>
              <span className="text-xs text-muted-foreground">{formatRelative(following)}</span>
            </span>
          ) : (
            <span className="text-sm text-muted-foreground">{T.resultingUnknown}</span>
          )}
        </li>
      </ol>
    </ActionFrame>
  );
}
