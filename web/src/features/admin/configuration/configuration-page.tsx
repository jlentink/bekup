import { useApolloClient, useMutation, useQuery } from "@apollo/client/react";
import {
  Database,
  FileCog,
  Loader2,
  RotateCw,
  Server,
  ShieldAlert,
  Terminal,
  TriangleAlert,
  Zap,
} from "lucide-react";
import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";

import { Tag } from "@/components/status/tag";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
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
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Section } from "@/features/account/section";
import { isStepUpCancelled, useStepUp } from "@/features/auth/use-step-up";
import { getFragmentData } from "@/graphql/gen";
import type {
  BekupProcessKind,
  DatabaseConfigMode,
  ConfigReloadErrorKind,
  ConfigReloadRequestFieldsFragment,
  ConfigReloadStatus,
} from "@/graphql/gen/graphql";
import { ApiCode, hasApiCode, userMessage } from "@/lib/api-error";
import { formatDateTime, formatRelative } from "@/lib/format";
import { messages } from "@/lib/messages";
import { cn } from "@/lib/utils";

import {
  ConfigProcessResultFields,
  ConfigReloadRequestFields,
  ConfigReloadRequestQuery,
  ConfigStatusQuery,
  ReloadConfigMutation,
} from "../operations";
import { docs } from "../docs";
import { Command, DocLink } from "../ui";
import {
  complete,
  expected,
  groupKeys,
  originOf,
  OUTCOME_TONE,
  OUTCOMES,
  POLL,
  splitByListening,
  STATUS_TONE,
  summarize,
  verdict,
  VERDICT_TONE,
  weakened,
  type Origin,
  type Outcome,
  type ProcessResult,
  type Verdict,
} from "./model";

const T = {
  title: "Configuration",
  lead: "Reload bekup.toml without restarting. Each serve and worker process re-reads its own file; live settings apply at once, everything else waits for a restart.",
  guide: "Reloading reference",
  stepUp: "Needs a step-up",
  reload: "Reload configuration",
  howTitle: "How a reload works",
  howLead:
    "Only key names are shown here, never values or the file path. Details are in each process's log.",
  how: [
    "Every serve and worker process re-reads the bekup.toml it started with.",
    "Live settings apply immediately; changed keys that are not live are reported as “restart required”.",
    "A file that cannot be read or is invalid is refused, and the running configuration stays in place.",
    "A change that weakens a security setting raises a security alert to every admin.",
  ],
  signalTitle: "One process only",
  signalLead:
    "Operators can also send SIGHUP to a single process. It reloads alone and its result appears in the table below.",
  signalCommand: "docker compose kill -s HUP serve",
  processesTitle: "Processes",
  processesLead: "The newest result of every process, newest first.",
  counts: (listening: number, stopped: number) =>
    `${String(listening)} listening · ${String(stopped)} stopped`,
  countsNote:
    "Every process listening now is listed, however long it has been running. Processes that stopped or lost their database connection stay listed for 7 days after their last report.",
  noProcesses: "No process has reported yet.",
  stoppedTitle: "Stopped or not listening",
  stoppedLead:
    "These processes no longer receive reload requests. Their last result is history: nothing to restart or fix here.",
  notListening: "Not listening",
  lastResult: (status: string) => `Last result: ${status}`,
  stoppedSince: "Stopped since",
  colProcess: "Process",
  colStarted: "Running since",
  colResult: "Last result",
  colKeys: "Keys",
  version: (v: string) => `v${v}`,
  liveTitle: "Live settings",
  liveLead:
    "Applied by a reload without a restart. Every other key needs a restart of the process.",
  liveShow: (n: number) => `Show the ${String(n)} live ${n === 1 ? "key" : "keys"}`,
  liveNone: "This version has no live keys: every change needs a restart.",
  liveTopLevel: "Top level",
  kind: { SERVE: "serve", WORKER: "worker" } satisfies Record<BekupProcessKind, string>,
  status: {
    LOADED: "Started",
    APPLIED: "Applied",
    UNCHANGED: "Unchanged",
    RESTART_REQUIRED: "Restart required",
    REJECTED: "Rejected",
    FAILED: "Failed",
  } satisfies Record<ConfigReloadStatus, string>,
  errorKind: {
    FILE: "File unreadable or fails the ownership/mode checks",
    SYNTAX: "Not valid TOML",
    INVALID: "Unknown key or invalid value",
    APPLY: "A component refused the new values",
  } satisfies Record<ConfigReloadErrorKind, string>,
  origin: {
    startup: "At startup",
    latest: "Latest reload",
    ui: "Earlier reload",
    signal: "SIGHUP",
  } satisfies Record<Origin, string>,
  keys: {
    restart: "Restart needed",
    restartStale: "Changed, needed a restart",
    applied: "Applied",
    errors: "Errors",
    weakened: "Weakened",
  },
  noKeys: "No changed keys",
  outcome: {
    applied: (n: number) => `${String(n)} applied`,
    restart: (n: number) => `${String(n)} restart required`,
    unchanged: (n: number) => `${String(n)} unchanged`,
    rejected: (n: number) => `${String(n)} rejected`,
    failed: (n: number) => `${String(n)} failed`,
    stopped: (n: number) => `${String(n)} stopped since`,
  } satisfies Record<Outcome, (n: number) => string>,
  verdict: {
    problem: "Some processes refused the file or could not apply it",
    restart: "Applied where possible; some changes need a restart",
    applied: "Live settings applied",
    unchanged: "Nothing changed",
    stopped: "The processes that answered have stopped since",
    none: "Waiting for answers",
  } satisfies Record<Verdict, string>,
  running: "Reload in progress",
  last: "Latest reload",
  requested: (when: string) => `Requested ${when}`,
  progress: (n: number, m: number) =>
    `${String(n)} of ${String(m)} ${m === 1 ? "process" : "processes"} answered`,
  timedOut:
    "Not every process answered within 30 seconds. Busy or restarting processes catch up within 10 minutes; reload this page later to see their results.",
  pollFailed: "Could not load the answers",
  weakenedTitle: "Security settings were weakened",
  weakenedLead: "Every admin received a security alert for these keys.",
  dialogEyebrow: "Every serve and worker process",
  dialogTitle: "Reload the configuration?",
  dialogLead: "Each process re-reads its own bekup.toml now.",
  dialogPoints: [
    "Live settings apply immediately; other changed keys wait for a restart.",
    "An unreadable or invalid file is refused and the running configuration stays.",
    "Weakening a security setting alerts every admin.",
    "You confirm your identity before the request is sent.",
  ],
  confirm: "Reload",
  outcomeLabel: "Outcome per process",
  rateLimited: "A reload was just requested. Wait a few seconds and try again.",
  dbTitle: "App database",
  dbLead:
    "How the serve process that answered connects to bekup's own database. These settings need a restart.",
  dbMode: "Credentials",
  dbTLS: "TLS mode",
  dbModes: {
    URL: "Connection URL files",
    FIELDS: "Host and password files (external PostgreSQL)",
    SINGLE_ACCOUNT: "One account for everything",
  } satisfies Record<DatabaseConfigMode, string>,
  dbWeakened: "Weakened",
  dbStrict: "verify-full",
  dbTLSWeakTitle: "The database connection is not fully verified",
  dbTLSWeakLead:
    "database.tls_mode is weaker than verify-full: a network attacker could impersonate the database or read the traffic. Every start is audited and admins are alerted daily.",
  dbSingleTitle: "Single-account mode is on",
  dbSingleLead:
    "bekup runs migrations and the app with one database account, so a compromised server process could change the schema and the audit log. Create the two least-privilege roles with bekup db bootstrap.",
} as const;

interface DatabasePosture {
  mode: DatabaseConfigMode;
  tlsMode: string;
  insecureTransport: boolean;
  singleAccount: boolean;
}

/** App database posture (D-100, D-207): mode, TLS and the weakenings. */
function DatabaseSection({ posture }: { posture: DatabasePosture | undefined }) {
  const weak = posture ? posture.insecureTransport || posture.singleAccount : false;
  return (
    <Section
      icon={Database}
      title={T.dbTitle}
      lead={T.dbLead}
      status={weak ? <Tag tone="insecure">{T.dbWeakened}</Tag> : null}
    >
      {!posture ? (
        <Skeleton className="h-8 w-full" />
      ) : (
        <div className="flex flex-col gap-4" data-testid="config-database">
          <dl className="grid gap-x-8 gap-y-3 text-sm sm:grid-cols-[auto_1fr]">
            <dt className="eyebrow self-center">{T.dbMode}</dt>
            <dd className="flex flex-wrap items-center gap-2">
              {posture.singleAccount ? (
                <Tag tone="insecure">{T.dbModes[posture.mode]}</Tag>
              ) : (
                <span>{T.dbModes[posture.mode]}</span>
              )}
            </dd>
            <dt className="eyebrow self-center">{T.dbTLS}</dt>
            <dd className="flex flex-wrap items-center gap-2">
              <code className="font-mono text-xs">{posture.tlsMode}</code>
              {posture.insecureTransport ? (
                <Tag tone="insecure">{T.dbWeakened}</Tag>
              ) : (
                <Tag tone="success">{T.dbStrict}</Tag>
              )}
            </dd>
          </dl>
          {posture.insecureTransport ? (
            <Alert variant="destructive">
              <ShieldAlert aria-hidden="true" />
              <AlertTitle>{T.dbTLSWeakTitle}</AlertTitle>
              <AlertDescription>{T.dbTLSWeakLead}</AlertDescription>
            </Alert>
          ) : null}
          {posture.singleAccount ? (
            <Alert variant="destructive">
              <TriangleAlert aria-hidden="true" />
              <AlertTitle>{T.dbSingleTitle}</AlertTitle>
              <AlertDescription>{T.dbSingleLead}</AlertDescription>
            </Alert>
          ) : null}
        </div>
      )}
    </Section>
  );
}

type Request = ConfigReloadRequestFieldsFragment;
type Phase = "polling" | "done" | "timeout" | "error";

interface Run {
  request: Request;
  phase: Phase;
  error?: string;
}

function results(req: Request): ProcessResult[] {
  return req.results.map((r) => getFragmentData(ConfigProcessResultFields, r));
}

/** Runtime config reload (task 6.13, D-200): processes, live keys, reload + poll. */
export function ConfigurationPage() {
  const { data, error, refetch } = useQuery(ConfigStatusQuery, {
    fetchPolicy: "cache-and-network",
  });
  const status = data?.configStatus;
  const [confirm, setConfirm] = useState(false);
  const refresh = useCallback(() => {
    void refetch().catch(() => undefined);
  }, [refetch]);
  const { run, start } = useReloadPoll(refresh);

  const processes = status?.processes.map((p) => getFragmentData(ConfigProcessResultFields, p));
  const groups = processes ? splitByListening(processes) : null;
  const lastRequest = status?.lastRequest
    ? getFragmentData(ConfigReloadRequestFields, status.lastRequest)
    : null;
  const shown: Run | null = run ?? (lastRequest ? { request: lastRequest, phase: "done" } : null);

  return (
    <div className="flex flex-col">
      <div className="flex flex-wrap items-start justify-between gap-3 border-b border-rule pb-4">
        <div className="flex max-w-prose flex-col gap-1">
          <h2 className="font-display text-3xl">{T.title}</h2>
          <p className="text-sm text-muted-foreground">{T.lead}</p>
          <DocLink href={docs.configReload}>{T.guide}</DocLink>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Tag tone="neutral">{T.stepUp}</Tag>
          <Button
            disabled={!status || run?.phase === "polling"}
            onClick={() => {
              setConfirm(true);
            }}
          >
            {run?.phase === "polling" ? (
              <Loader2 aria-hidden="true" className="animate-spin" />
            ) : (
              <RotateCw aria-hidden="true" />
            )}
            {T.reload}
          </Button>
        </div>
      </div>

      {error && !data ? (
        <Alert variant="destructive" className="mt-6">
          <AlertDescription className="flex flex-wrap items-center gap-3">
            {userMessage(error)}
            <Button variant="outline" size="sm" onClick={refresh}>
              {messages.common.retry}
            </Button>
          </AlertDescription>
        </Alert>
      ) : null}

      {shown ? <ReloadPanel run={shown} live={run !== null} /> : null}

      <Section icon={FileCog} title={T.howTitle} lead={T.howLead}>
        <ol className="grid gap-2 text-sm">
          {T.how.map((p, i) => (
            <li key={p} className="flex items-baseline gap-3">
              <span
                aria-hidden="true"
                className="font-mono text-2xs text-muted-foreground tabular-nums"
              >
                {String(i + 1).padStart(2, "0")}
              </span>
              {p}
            </li>
          ))}
        </ol>
        <div className="flex flex-col gap-1.5 border-t border-rule pt-4">
          <p className="eyebrow flex items-center gap-1.5">
            <Terminal aria-hidden="true" className="size-3.5" />
            {T.signalTitle}
          </p>
          <p className="text-sm text-muted-foreground">{T.signalLead}</p>
          <Command>{T.signalCommand}</Command>
        </div>
      </Section>

      <Section
        icon={Server}
        title={T.processesTitle}
        lead={T.processesLead}
        status={
          groups ? (
            <Tag tone="neutral" title={T.countsNote}>
              <span data-testid="config-process-counts">
                {T.counts(groups.listening.length, groups.stopped.length)}
              </span>
            </Tag>
          ) : null
        }
      >
        {!processes || !groups ? (
          <div className="flex flex-col gap-2" aria-busy="true">
            <Skeleton className="h-12 w-full" />
            <Skeleton className="h-12 w-full" />
          </div>
        ) : processes.length === 0 ? (
          <p className="text-sm text-muted-foreground">{T.noProcesses}</p>
        ) : (
          <ProcessTable
            listening={groups.listening}
            stopped={groups.stopped}
            lastRequestId={lastRequest?.id}
          />
        )}
        {status ? <p className="text-xs text-muted-foreground">{T.countsNote}</p> : null}
      </Section>

      <DatabaseSection posture={status?.database} />

      <Section icon={Zap} title={T.liveTitle} lead={T.liveLead}>
        {!status ? (
          <Skeleton className="h-8 w-full" />
        ) : status.liveKeys.length === 0 ? (
          <p className="text-sm text-muted-foreground">{T.liveNone}</p>
        ) : (
          <LiveKeys keys={status.liveKeys} />
        )}
      </Section>

      {confirm ? (
        <ReloadDialog
          onClose={() => {
            setConfirm(false);
          }}
          onStarted={(req) => {
            setConfirm(false);
            start(req);
          }}
        />
      ) : null}
    </div>
  );
}

/**
 * Polls configReloadRequest every POLL.intervalMs until the expected
 * processes (expectedProcesses) answered or POLL.timeoutMs passed; calls
 * onFinish once at the end.
 */
function useReloadPoll(onFinish: () => void) {
  const client = useApolloClient();
  const [run, setRun] = useState<Run | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const alive = useRef(true);

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
      clearTimeout(timer.current);
    };
  }, []);

  const start = useCallback(
    (first: Request) => {
      clearTimeout(timer.current);
      const deadline = Date.now() + POLL.timeoutMs;
      const settle = (request: Request): boolean => {
        if (complete(request.results.length, request.expectedProcesses)) {
          setRun({ request, phase: "done" });
          onFinish();
          return true;
        }
        if (Date.now() >= deadline) {
          setRun({ request, phase: "timeout" });
          onFinish();
          return true;
        }
        setRun({ request, phase: "polling" });
        return false;
      };
      const tick = (current: Request) => {
        timer.current = setTimeout(() => {
          client
            .query({
              query: ConfigReloadRequestQuery,
              variables: { id: current.id },
              fetchPolicy: "network-only",
            })
            .then((r) => {
              if (!alive.current) return;
              const raw = r.data?.configReloadRequest;
              const next = raw ? getFragmentData(ConfigReloadRequestFields, raw) : current;
              if (!settle(next)) tick(next);
            })
            .catch((err: unknown) => {
              if (!alive.current) return;
              setRun({ request: current, phase: "error", error: userMessage(err) });
              onFinish();
            });
        }, POLL.intervalMs);
      };
      if (!settle(first)) tick(first);
    },
    [client, onFinish],
  );

  return { run, start };
}

function ReloadDialog({
  onClose,
  onStarted,
}: {
  onClose: () => void;
  onStarted: (req: Request) => void;
}) {
  const { withStepUp } = useStepUp();
  const [reload] = useMutation(ReloadConfigMutation);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  const submit = () => {
    setBusy(true);
    setError(undefined);
    withStepUp(() => reload())
      .then((r) => {
        const raw = r.data?.reloadConfig;
        if (raw) onStarted(getFragmentData(ConfigReloadRequestFields, raw));
      })
      .catch((err: unknown) => {
        if (isStepUpCancelled(err)) return;
        setError(hasApiCode(err, ApiCode.rateLimited) ? T.rateLimited : userMessage(err));
      })
      .finally(() => {
        setBusy(false);
      });
  };

  return (
    <Dialog
      open
      onOpenChange={(o) => {
        if (!o && !busy) onClose();
      }}
    >
      <DialogContent className="gap-5 sm:max-w-lg">
        <DialogHeader>
          <p className="eyebrow flex items-center gap-1.5">
            <RotateCw aria-hidden="true" className="size-3.5" /> {T.dialogEyebrow}
          </p>
          <DialogTitle className="font-display text-2xl font-normal">{T.dialogTitle}</DialogTitle>
          <DialogDescription>{T.dialogLead}</DialogDescription>
        </DialogHeader>
        <ul className="grid gap-2 border-l-2 border-primary/40 pl-4 text-sm">
          {T.dialogPoints.map((p) => (
            <li key={p}>{p}</li>
          ))}
        </ul>
        {error ? (
          <Alert variant="destructive" data-testid="config-reload-error">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        ) : null}
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" disabled={busy}>
              {messages.common.cancel}
            </Button>
          </DialogClose>
          <Button disabled={busy} onClick={submit}>
            {busy ? (
              <Loader2 aria-hidden="true" className="animate-spin" />
            ) : (
              <RotateCw aria-hidden="true" />
            )}
            {T.confirm}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** Max segments in the progress bar; beyond that the text alone carries it. */
const MAX_SEGMENTS = 24;

function ReloadPanel({ run, live }: { run: Run; live: boolean }) {
  const { request, phase } = run;
  const list = results(request);
  const answered = list.length;
  const total = expected(answered, request.expectedProcesses);
  const summary = summarize(list);
  const v = phase === "polling" && answered === 0 ? "none" : verdict(summary);
  const weak = weakened(list);
  const polling = phase === "polling";

  return (
    <section
      aria-label={polling ? T.running : T.last}
      data-testid="config-reload-panel"
      className="mt-6 flex flex-col gap-4 rounded-md border border-rule bg-surface px-4 py-4 sm:px-5"
    >
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
        <p className="eyebrow flex items-center gap-1.5">
          {polling ? <Loader2 aria-hidden="true" className="size-3.5 animate-spin" /> : null}
          {polling ? T.running : T.last}
        </p>
        <time
          dateTime={request.requestedAt}
          title={formatDateTime(request.requestedAt)}
          className="text-xs text-muted-foreground"
        >
          {T.requested(formatRelative(request.requestedAt))}
        </time>
      </div>

      <div className="flex flex-col gap-2">
        <p
          role="status"
          aria-live={live ? "polite" : "off"}
          className="font-mono text-sm tabular-nums"
          data-testid="config-reload-progress"
        >
          {T.progress(answered, total)}
        </p>
        {total > 0 && total <= MAX_SEGMENTS ? (
          <div aria-hidden="true" className="flex gap-1">
            {Array.from({ length: total }, (_, i) => (
              <span
                key={i}
                className={cn(
                  "h-1.5 flex-1 rounded-full transition-colors",
                  i < answered ? "bg-primary" : "bg-surface-sunken",
                  i === answered && polling && "animate-pulse bg-muted-foreground/30",
                )}
              />
            ))}
          </div>
        ) : null}
      </div>

      {answered > 0 ? (
        <div className="flex flex-col gap-2" data-testid="config-reload-summary">
          <p className="flex items-center gap-2 text-[0.9375rem] font-semibold tracking-tight">
            <Tag tone={VERDICT_TONE[v]} compact>
              {T.verdict[v]}
            </Tag>
          </p>
          <ul className="flex flex-wrap gap-1.5" aria-label={T.outcomeLabel}>
            {OUTCOMES.filter((o) => summary[o] > 0).map((o) => (
              <li key={o}>
                <Tag tone={OUTCOME_TONE[o]}>{T.outcome[o](summary[o])}</Tag>
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      {weak.length > 0 ? <WeakenedRow keys={weak} /> : null}

      {answered > 0 ? (
        <ul className="flex flex-col divide-y divide-rule border-t border-rule">
          {list.map((r) => (
            <li
              key={r.instanceId}
              data-testid="config-reload-result"
              data-listening={r.listening}
              className={cn(
                "grid gap-2 py-3 sm:grid-cols-[minmax(0,14rem)_minmax(0,1fr)] sm:gap-4",
                !r.listening && "opacity-70",
              )}
            >
              <div className="flex min-w-0 flex-col gap-1">
                <ProcessName result={r} />
                <span className="flex flex-wrap items-center gap-1.5">
                  {r.listening ? (
                    <StatusTag status={r.status} />
                  ) : (
                    <>
                      <Tag tone="neutral" compact>
                        {T.stoppedSince}
                      </Tag>
                      <span className="text-xs text-muted-foreground">
                        {T.lastResult(T.status[r.status])}
                      </span>
                    </>
                  )}
                </span>
              </div>
              <ProcessKeys result={r} stale={!r.listening} />
            </li>
          ))}
        </ul>
      ) : null}

      {phase === "timeout" ? (
        <Alert>
          <TriangleAlert aria-hidden="true" />
          <AlertDescription>{T.timedOut}</AlertDescription>
        </Alert>
      ) : null}
      {phase === "error" ? (
        <Alert variant="destructive">
          <AlertTitle>{T.pollFailed}</AlertTitle>
          <AlertDescription>{run.error}</AlertDescription>
        </Alert>
      ) : null}
    </section>
  );
}

function WeakenedRow({ keys }: { keys: string[] }) {
  return (
    <div
      role="note"
      data-testid="config-weakened"
      className="hatch-insecure flex gap-3 rounded-md border border-status-insecure/50 px-4 py-3 text-status-insecure-fg"
    >
      <ShieldAlert aria-hidden="true" className="mt-0.5 size-5 shrink-0" />
      <div className="flex min-w-0 flex-col gap-1.5">
        <p className="font-semibold">{T.weakenedTitle}</p>
        <KeyChips keys={keys} tone="insecure" label={T.keys.weakened} />
        <p className="text-sm">{T.weakenedLead}</p>
      </div>
    </div>
  );
}

function StatusTag({ status }: { status: ConfigReloadStatus }) {
  return (
    <Tag tone={STATUS_TONE[status]} compact>
      {T.status[status]}
    </Tag>
  );
}

function ProcessName({ result }: { result: ProcessResult }) {
  return (
    <span className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5">
      <span className="font-mono text-2xs tracking-[0.08em] text-muted-foreground uppercase">
        {T.kind[result.kind]}
      </span>
      <span className="truncate font-mono text-sm font-medium" title={result.hostname}>
        {result.hostname}
      </span>
      <span className="font-mono text-2xs text-muted-foreground">{T.version(result.version)}</span>
    </span>
  );
}

const CHIP_TONE: Record<"neutral" | "warning" | "danger" | "insecure", string> = {
  neutral: "border-border bg-surface-sunken text-foreground",
  warning: "border-status-partial/45 bg-status-partial-soft text-status-partial-fg",
  danger: "border-status-failed/45 bg-status-failed-soft text-status-failed-fg",
  insecure: "border-status-insecure/50 bg-surface text-status-insecure-fg",
};

function KeyChips({
  keys,
  tone,
  label,
}: {
  keys: readonly string[];
  tone: keyof typeof CHIP_TONE;
  label: string;
}) {
  return (
    <ul aria-label={label} className="flex flex-wrap gap-1">
      {keys.map((k) => (
        <li
          key={k}
          className={cn(
            "rounded-sm border px-1.5 py-px font-mono text-xs break-all",
            CHIP_TONE[tone],
          )}
        >
          {k}
        </li>
      ))}
    </ul>
  );
}

function KeyGroupRow({
  label,
  keys,
  tone,
  icon,
  note,
}: {
  label: string;
  keys: readonly string[];
  tone: keyof typeof CHIP_TONE;
  icon?: ReactNode;
  note?: string | undefined;
}) {
  const labelTone: Record<keyof typeof CHIP_TONE, string> = {
    neutral: "text-muted-foreground",
    warning: "text-status-partial-fg",
    danger: "text-status-failed-fg",
    insecure: "text-status-insecure-fg",
  };
  return (
    <div className="flex flex-col gap-1">
      <span className={cn("eyebrow flex items-center gap-1", labelTone[tone])}>
        {icon}
        {label}
      </span>
      {note ? <span className={cn("text-xs", labelTone[tone])}>{note}</span> : null}
      {keys.length > 0 ? <KeyChips keys={keys} tone={tone} label={label} /> : null}
    </div>
  );
}

/**
 * The keys of one result: restart needed, errors, weakened, applied. A
 * stale result (the process no longer listens) is history: every group is
 * neutral and nothing asks for a restart.
 */
function ProcessKeys({ result: r, stale = false }: { result: ProcessResult; stale?: boolean }) {
  const hasError = r.errorKind != null || r.errorKeys.length > 0;
  if (
    !hasError &&
    r.restartKeys.length === 0 &&
    r.weakenedKeys.length === 0 &&
    r.appliedKeys.length === 0
  ) {
    return <span className="text-xs text-muted-foreground">{T.noKeys}</span>;
  }
  return (
    <div className="flex min-w-0 flex-col gap-2.5">
      {hasError ? (
        <KeyGroupRow
          label={T.keys.errors}
          keys={r.errorKeys}
          tone={stale ? "neutral" : "danger"}
          note={r.errorKind ? T.errorKind[r.errorKind] : undefined}
        />
      ) : null}
      {r.weakenedKeys.length > 0 ? (
        <KeyGroupRow
          label={T.keys.weakened}
          keys={r.weakenedKeys}
          tone={stale ? "neutral" : "insecure"}
          icon={<ShieldAlert aria-hidden="true" className="size-3" />}
        />
      ) : null}
      {r.restartKeys.length > 0 ? (
        <KeyGroupRow
          label={stale ? T.keys.restartStale : T.keys.restart}
          keys={r.restartKeys}
          tone={stale ? "neutral" : "warning"}
        />
      ) : null}
      {r.appliedKeys.length > 0 ? (
        <KeyGroupRow label={T.keys.applied} keys={r.appliedKeys} tone="neutral" />
      ) : null}
    </div>
  );
}

function ProcessTable({
  listening,
  stopped,
  lastRequestId,
}: {
  listening: ProcessResult[];
  stopped: ProcessResult[];
  lastRequestId: string | undefined;
}) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>{T.colProcess}</TableHead>
          <TableHead>{T.colStarted}</TableHead>
          <TableHead>{T.colResult}</TableHead>
          <TableHead>{T.colKeys}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {listening.map((p) => (
          <ProcessRow key={p.instanceId} p={p} lastRequestId={lastRequestId} />
        ))}
      </TableBody>
      {stopped.length > 0 ? (
        <TableBody data-testid="config-stopped-processes" aria-label={T.stoppedTitle}>
          <TableRow className="hover:bg-transparent">
            <TableCell colSpan={4} className="border-t border-rule pt-5 whitespace-normal">
              <p className="eyebrow">{T.stoppedTitle}</p>
              <p className="text-xs text-muted-foreground">{T.stoppedLead}</p>
            </TableCell>
          </TableRow>
          {stopped.map((p) => (
            <ProcessRow key={p.instanceId} p={p} lastRequestId={lastRequestId} stale />
          ))}
        </TableBody>
      ) : null}
    </Table>
  );
}

function ProcessRow({
  p,
  lastRequestId,
  stale = false,
}: {
  p: ProcessResult;
  lastRequestId: string | undefined;
  stale?: boolean;
}) {
  return (
    <TableRow
      data-testid={stale ? "config-process-stopped" : "config-process"}
      className={cn("align-top", stale && "text-muted-foreground opacity-70")}
    >
      <TableCell className="max-w-[16rem] whitespace-normal">
        <ProcessName result={p} />
      </TableCell>
      <TableCell className="whitespace-nowrap">
        <time
          dateTime={p.processStartedAt}
          title={formatDateTime(p.processStartedAt)}
          className="text-xs text-muted-foreground"
        >
          {formatRelative(p.processStartedAt)}
        </time>
      </TableCell>
      <TableCell className="whitespace-nowrap">
        <div className="flex flex-col items-start gap-1">
          {stale ? (
            <>
              <Tag tone="neutral" compact>
                {T.notListening}
              </Tag>
              <span className="text-xs text-muted-foreground">
                {T.lastResult(T.status[p.status])}
              </span>
            </>
          ) : (
            <StatusTag status={p.status} />
          )}
          <span className="text-xs text-muted-foreground">
            {T.origin[originOf(p, lastRequestId)]} ·{" "}
            <time dateTime={p.finishedAt} title={formatDateTime(p.finishedAt)}>
              {formatRelative(p.finishedAt)}
            </time>
          </span>
        </div>
      </TableCell>
      <TableCell className="min-w-[14rem] whitespace-normal">
        <ProcessKeys result={p} stale={stale} />
      </TableCell>
    </TableRow>
  );
}

function LiveKeys({ keys }: { keys: readonly string[] }) {
  const groups = groupKeys(keys);
  return (
    <details className="group rounded-md border border-rule" data-testid="config-live-keys">
      <summary className="cursor-pointer list-none px-3 py-2 text-sm font-medium select-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none [&::-webkit-details-marker]:hidden">
        <span className="inline-flex items-center gap-2">
          <span
            aria-hidden="true"
            className="font-mono text-xs text-muted-foreground transition-transform group-open:rotate-90"
          >
            ›
          </span>
          {T.liveShow(keys.length)}
        </span>
      </summary>
      <dl className="grid gap-3 border-t border-rule px-3 py-3">
        {groups.map((g) => (
          <div key={g.section} className="grid gap-1 sm:grid-cols-[10rem_minmax(0,1fr)] sm:gap-3">
            <dt className="eyebrow pt-0.5">{g.section || T.liveTopLevel}</dt>
            <dd>
              <KeyChips keys={g.keys} tone="neutral" label={g.section || T.liveTopLevel} />
            </dd>
          </div>
        ))}
      </dl>
    </details>
  );
}
