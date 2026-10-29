import { useMutation, useQuery } from "@apollo/client/react";
import { Loader2, ShieldAlert } from "lucide-react";
import { useCallback, useState } from "react";
import { Link } from "react-router";
import { toast } from "sonner";

import { Tag } from "@/components/status/tag";
import { Button } from "@/components/ui/button";
import { isStepUpCancelled, useStepUp } from "@/features/auth/use-step-up";
import { PRIVILEGE_META, privilegeReason } from "@/features/connections/labels";

import type {
  DatabaseEngine,
  JobDetailQuery as JobDetailResult,
  TargetPrivilegeStatus,
} from "@/graphql/gen/graphql";
import { ApiCode, firstApiError, hasApiCode, userMessage } from "@/lib/api-error";
import { formatDateTime, formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

import { CheckboxField, Field, NativeSelect } from "./fields";
import { SetJobTestRestoreMutation, TestRestoreTargetsQuery } from "./operations";
import { draftFromSchedule, draftToInput, type ScheduleDraft } from "./schedule";
import { ScheduleBuilder } from "./schedule-builder";

type Job = NonNullable<JobDetailResult["job"]>;

interface Verdict {
  status: TargetPrivilegeStatus;
  reasons: readonly string[];
  acknowledgementRequired: boolean;
}

/** Where the verdict shown for the chosen target comes from. */
type VerdictSource =
  | { kind: "designation"; verdict: Verdict }
  | { kind: "lastTest"; verdict: Verdict; at: string; stale: boolean }
  | { kind: "none" };

const T = {
  enabled: "Scheduled test restores",
  enabledHint:
    "Regularly restore the newest verified backup into a scratch database, compare it with the source statistics and drop it again.",
  target: "Scratch target",
  targetNone: "No scratch target",
  targetHint: (engine: string) =>
    `A permanent ${engine} connection. bekup only ever creates and drops bekup_test_<id> databases there; it receives a copy of this job's data.`,
  targetRequired: "Choose a scratch target to turn test restores on.",
  notReady: "not ready",
  schedule: "When test restores run",
  verdict: "Target role",
  fromDesignation: "probed when it was designated",
  fromTest: (when: string, stale: boolean) =>
    `from the connection's last test, ${when}${stale ? " (stale)" : ""}`,
  untested:
    "Not tested yet. The role is probed when you save; a privileged or unknown role needs an admin and an acknowledgement.",
  ack: "I accept that SQL in the tested backups runs with this role's rights on the scratch target",
  ackHint:
    "Required for a privileged or unknown role (D-068, D-170). Audited and alerted; the worker re-probes the role before every test restore.",
  ackedAt: (at: string) => `Acknowledged by an admin ${at}.`,
  maintainerGated:
    "This role is privileged or unknown: only an admin can designate it as a scratch target.",
  stepUpNote:
    "Saving needs a passkey or authenticator code from the last 5 minutes: the scratch target receives a copy of the job's data.",
  save: "Save test-restore settings",
  saved: "Test-restore settings saved.",
  forbidden: "Only an admin can designate a target whose role is privileged or unknown.",
  testConnection: "Test the connection",
} as const;

const ENGINE: Record<DatabaseEngine, string> = {
  POSTGRES: "PostgreSQL",
  MYSQL: "MySQL",
  MARIADB: "MariaDB",
};

/**
 * The job's test-restore settings (task 4.6, D-169/D-170): on/off, the
 * schedule, the scratch target (a permanent connection with the job's
 * engine) and, for a privileged or unknown target role, the admin's
 * acknowledgement. setJobTestRestore needs a fresh second factor (AAL2).
 * The server probes the target on save; the verdict shown here is the
 * designation's or the connection's stored last test (D-187).
 */
export function TestRestoreSettings({ job, admin }: { job: Job; admin: boolean }) {
  const tr = job.testRestore;
  const { withStepUp } = useStepUp();
  const targets = useQuery(TestRestoreTargetsQuery, { fetchPolicy: "cache-and-network" });
  const [save, saveState] = useMutation(SetJobTestRestoreMutation);
  const [enabled, setEnabled] = useState(tr.enabled);
  const [targetId, setTargetId] = useState(tr.target?.id ?? "");
  const [draft, setDraft] = useState<ScheduleDraft>(() => draftFromSchedule(tr.schedule));
  const [scheduleProblem, setScheduleProblem] = useState<string | null>(null);
  const [ack, setAck] = useState(false);
  const [error, setError] = useState<string>();
  const onValidity = useCallback((p: string | null) => {
    setScheduleProblem(p);
  }, []);

  const engine = job.connection.engine;
  const candidates = (targets.data?.connections ?? []).filter(
    (c) => !c.temporary && c.engine === engine,
  );
  const chosen = candidates.find((c) => c.id === targetId);
  const source: VerdictSource =
    targetId && targetId === tr.target?.id && tr.targetPrivileges
      ? { kind: "designation", verdict: tr.targetPrivileges }
      : chosen?.lastTest?.privileges
        ? {
            kind: "lastTest",
            verdict: chosen.lastTest.privileges,
            at: chosen.lastTest.at,
            stale: chosen.lastTest.stale,
          }
        : { kind: "none" };
  // Gated: privileged or unknown, or not known yet (the server decides on save).
  const gated =
    targetId !== "" && (source.kind === "none" || source.verdict.acknowledgementRequired);
  const alreadyAcked =
    source.kind === "designation" &&
    tr.privilegedTargetAcknowledgedAt != null &&
    targetId === tr.target?.id;

  const missingTarget = enabled && targetId === "";
  const canSubmit = !saveState.loading && !missingTarget && (!enabled || scheduleProblem === null);

  const submit = async () => {
    setError(undefined);
    try {
      await withStepUp(
        () =>
          save({
            variables: {
              jobId: job.id,
              input: {
                enabled,
                schedule: draftToInput(draft),
                targetConnectionId: targetId || null,
                ...(admin && ack ? { acknowledgePrivilegedTarget: true } : {}),
              },
            },
          }),
        { aal2: true },
      );
      setAck(false);
      toast.success(T.saved);
    } catch (err) {
      if (isStepUpCancelled(err)) return;
      setError(
        hasApiCode(err, ApiCode.forbidden)
          ? (firstApiError(err)?.message ?? T.forbidden)
          : userMessage(err),
      );
    }
  };

  return (
    <form
      className="flex flex-col gap-5"
      aria-label="Test-restore settings"
      onSubmit={(e) => {
        e.preventDefault();
        void submit();
      }}
    >
      <CheckboxField
        label={T.enabled}
        hint={T.enabledHint}
        checked={enabled}
        onChange={(e) => {
          setEnabled(e.target.checked);
        }}
      />

      <Field
        label={T.target}
        hint={T.targetHint(ENGINE[engine])}
        error={missingTarget ? T.targetRequired : null}
      >
        {({ id, describedBy, invalid }) => (
          <NativeSelect
            id={id}
            aria-describedby={describedBy}
            aria-invalid={invalid || undefined}
            value={targetId}
            onChange={(e) => {
              setTargetId(e.target.value);
              setAck(false);
              setError(undefined);
            }}
          >
            <option value="">{T.targetNone}</option>
            {candidates.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
                {c.ready ? "" : ` (${T.notReady})`}
              </option>
            ))}
            {tr.target && !candidates.some((c) => c.id === tr.target?.id) ? (
              <option value={tr.target.id}>{tr.target.name}</option>
            ) : null}
          </NativeSelect>
        )}
      </Field>

      {targetId ? <VerdictPanel source={source} connectionId={targetId} /> : null}

      {gated && admin ? (
        <div className="flex flex-col gap-1.5">
          {alreadyAcked && tr.privilegedTargetAcknowledgedAt ? (
            <p className="text-xs text-muted-foreground">
              {T.ackedAt(formatDateTime(tr.privilegedTargetAcknowledgedAt))}
            </p>
          ) : null}
          <CheckboxField
            label={T.ack}
            hint={T.ackHint}
            checked={ack}
            onChange={(e) => {
              setAck(e.target.checked);
            }}
          />
        </div>
      ) : null}
      {gated && !admin && source.kind !== "none" ? (
        <p className="flex items-start gap-2 text-sm text-status-partial-fg">
          <ShieldAlert aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
          {T.maintainerGated}
        </p>
      ) : null}

      {enabled ? (
        <div className="flex flex-col gap-2">
          <p className="text-sm font-medium">{T.schedule}</p>
          <ScheduleBuilder value={draft} onChange={setDraft} onValidity={onValidity} />
        </div>
      ) : null}

      {error ? (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      ) : null}
      <p className="text-xs text-muted-foreground">{T.stepUpNote}</p>
      <Button type="submit" variant="outline" className="self-start" disabled={!canSubmit}>
        {saveState.loading ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
        {T.save}
      </Button>
    </form>
  );
}

function VerdictPanel({ source, connectionId }: { source: VerdictSource; connectionId: string }) {
  if (source.kind === "none") {
    return (
      <p className="text-sm text-muted-foreground" data-testid="target-verdict">
        {T.untested}{" "}
        <Link
          to={`/connections/${connectionId}`}
          className="text-primary underline-offset-4 hover:underline"
        >
          {T.testConnection}
        </Link>
      </p>
    );
  }
  const meta = PRIVILEGE_META[source.verdict.status];
  return (
    <div
      data-testid="target-verdict"
      className={cn(
        "flex flex-col gap-2 rounded-md border border-l-2 bg-surface px-3 py-2.5",
        source.verdict.acknowledgementRequired
          ? "border-l-status-partial"
          : "border-l-status-success",
      )}
    >
      <div className="flex flex-wrap items-center gap-2">
        <span className="eyebrow">{T.verdict}</span>
        <Tag tone={meta.tone}>{meta.label}</Tag>
        <span className="text-2xs text-muted-foreground">
          {source.kind === "designation"
            ? T.fromDesignation
            : T.fromTest(formatRelative(source.at), source.stale)}
        </span>
      </div>
      {source.verdict.acknowledgementRequired ? (
        <p className="text-xs text-muted-foreground">{meta.description}</p>
      ) : null}
      {source.verdict.reasons.length > 0 ? (
        <ul aria-label="Privilege reasons" className="flex flex-wrap gap-1.5">
          {source.verdict.reasons.map((r) => (
            <li
              key={r}
              title={r}
              className="rounded-sm border bg-surface-sunken px-1.5 py-0.5 font-mono text-2xs"
            >
              {privilegeReason(r)}
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
