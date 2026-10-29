import { useMutation, useQuery } from "@apollo/client/react";
import { BellRing, Info, Layers, RotateCcw, X } from "lucide-react";
import { useEffect, useId, useMemo, useState } from "react";
import { useLocation } from "react-router";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";
import { Skeleton } from "@/components/ui/skeleton";
import { Section } from "@/features/account/section";
import type { NotificationChannelType } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";

import { ChannelsSection } from "./channels-section";
import { notificationsMessages as M } from "./messages";
import {
  GROUP_ORDER,
  jobsWithOverrides,
  linkedJob,
  replaceRow,
  unmaskPrefs,
  unmaskRow,
  type PreferenceRow,
  type Prefs,
} from "./model";
import {
  MyNotificationPreferencesQuery,
  NotificationJobOptionsQuery,
  ResetNotificationPreferencesMutation,
  SetNotificationPreferenceMutation,
} from "./operations";
import { GroupMatrix, JobOverrideMatrix, type ToggleHandler } from "./preference-matrix";

interface Job {
  jobId: string;
  jobName: string;
}

function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => {
      setV(value);
    }, ms);
    return () => {
      clearTimeout(t);
    };
  }, [value, ms]);
  return v;
}

/** Picks a job the caller can see (jobs query, by name). */
function JobPicker({ onPick }: { onPick: (job: Job) => void }) {
  const searchId = useId();
  const selectId = useId();
  const [search, setSearch] = useState("");
  const [chosen, setChosen] = useState("");
  const term = useDebounced(search.trim(), 250);
  const { data, loading } = useQuery(NotificationJobOptionsQuery, {
    variables: { search: term === "" ? null : term },
  });
  const jobs = (data?.jobs.edges ?? []).map((e) => e.node);
  const selected = jobs.find((j) => j.id === chosen) ?? null;
  return (
    <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] sm:items-end">
      <div className="flex flex-col gap-1.5">
        <label htmlFor={searchId} className="eyebrow">
          {M.overrides.search}
        </label>
        <Input
          id={searchId}
          type="search"
          value={search}
          placeholder={M.overrides.searchPlaceholder}
          onChange={(e) => {
            setSearch(e.target.value);
          }}
        />
      </div>
      <div className="flex flex-col gap-1.5">
        <label htmlFor={selectId} className="eyebrow">
          {M.overrides.pick}
        </label>
        <NativeSelect
          id={selectId}
          value={selected ? selected.id : ""}
          disabled={loading && !data}
          onChange={(e) => {
            setChosen(e.target.value);
          }}
        >
          <option value="">{jobs.length === 0 ? M.overrides.noJobs : M.overrides.pick}</option>
          {jobs.map((j) => (
            <option key={j.id} value={j.id}>
              {j.name}
            </option>
          ))}
        </NativeSelect>
      </div>
      <Button
        variant="outline"
        disabled={!selected}
        onClick={() => {
          if (selected) onPick({ jobId: selected.id, jobName: selected.name });
        }}
      >
        {M.overrides.add}
      </Button>
    </div>
  );
}

function OverridesSection({
  prefs,
  busy,
  onToggle,
  onReset,
}: {
  prefs: Prefs;
  busy: ReadonlySet<string>;
  onToggle: (
    row: PreferenceRow,
    channel: NotificationChannelType,
    v: boolean,
    jobId: string,
  ) => void;
  onReset: (job: Job) => Promise<void>;
}) {
  const location = useLocation();
  const [editing, setEditing] = useState<Job | null>(() =>
    linkedJob(
      location.search,
      location.state,
      jobsWithOverrides(prefs.groups.flatMap((g) => g.events).filter((r) => r.event.perJob)),
      M.overrides.thisJob,
    ),
  );
  const [confirm, setConfirm] = useState<Job | null>(null);
  const rows = useMemo(
    () => prefs.groups.flatMap((g) => g.events).filter((r) => r.event.perJob),
    [prefs],
  );
  const existing = jobsWithOverrides(rows);
  const toggle: ToggleHandler | null = editing
    ? (row, channel, v) => {
        onToggle(row, channel, v, editing.jobId);
      }
    : null;

  return (
    <Section icon={Layers} title={M.overrides.heading} lead={M.overrides.lead}>
      <JobPicker onPick={setEditing} />
      <div className="flex flex-col gap-2">
        <p className="eyebrow">{M.overrides.existing}</p>
        {existing.length === 0 ? (
          <p className="text-sm text-muted-foreground">{M.overrides.none}</p>
        ) : (
          <ul className="flex flex-wrap gap-2">
            {existing.map((j) => (
              <li key={j.jobId}>
                <Button
                  size="sm"
                  variant={editing?.jobId === j.jobId ? "default" : "outline"}
                  aria-pressed={editing?.jobId === j.jobId}
                  onClick={() => {
                    setEditing(j);
                  }}
                >
                  {j.jobName}
                </Button>
              </li>
            ))}
          </ul>
        )}
      </div>
      {editing && toggle ? (
        <div
          className="flex flex-col gap-3 rounded-md border bg-card px-4 py-4"
          aria-label={M.overrides.editing(editing.jobName)}
          role="region"
        >
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h3 className="font-display text-xl leading-none tracking-tight">
              {M.overrides.editing(editing.jobName)}
            </h3>
            <div className="flex items-center gap-1">
              <Button
                size="sm"
                variant="ghost"
                disabled={!existing.some((j) => j.jobId === editing.jobId)}
                onClick={() => {
                  setConfirm(editing);
                }}
              >
                <RotateCcw aria-hidden="true" /> {M.overrides.reset}
              </Button>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => {
                  setEditing(null);
                }}
              >
                <X aria-hidden="true" /> {M.overrides.close}
              </Button>
            </div>
          </div>
          <JobOverrideMatrix
            jobId={editing.jobId}
            jobName={editing.jobName}
            rows={rows}
            busy={busy}
            onToggle={toggle}
          />
        </div>
      ) : null}
      <ConfirmDialog
        open={confirm !== null}
        onOpenChange={(o) => {
          if (!o) setConfirm(null);
        }}
        title={confirm ? M.overrides.resetTitle(confirm.jobName) : ""}
        description={M.overrides.resetBody}
        confirmLabel={M.overrides.reset}
        destructive
        onConfirm={async () => {
          if (confirm) await onReset(confirm);
        }}
      />
    </Section>
  );
}

/**
 * Profile → Notifications (task 6.10): personal channels, the event ×
 * channel matrix (D-161) grouped by the catalog, locked security rows,
 * per-job overrides and reset to defaults.
 */
export function NotificationSettingsPage() {
  const { data, error, loading, refetch } = useQuery(MyNotificationPreferencesQuery, {
    fetchPolicy: "cache-and-network",
  });
  const [setPref] = useMutation(SetNotificationPreferenceMutation, { fetchPolicy: "no-cache" });
  const [reset] = useMutation(ResetNotificationPreferencesMutation, { fetchPolicy: "no-cache" });
  const [local, setLocal] = useState<{ from: unknown; prefs: Prefs } | null>(null);
  const [busy, setBusy] = useState<ReadonlySet<string>>(new Set());
  const [confirmReset, setConfirmReset] = useState(false);

  const server = useMemo(() => (data ? unmaskPrefs(data.myNotificationPreferences) : null), [data]);
  const prefs = local && local.from === data ? local.prefs : server;

  /** Applies a server answer on top of the latest local state (concurrent toggles). */
  const update = (next: (base: Prefs) => Prefs) => {
    setLocal((prev) => {
      const base = prev && prev.from === data ? prev.prefs : server;
      return base ? { from: data, prefs: next(base) } : prev;
    });
  };

  const toggle = async (
    row: PreferenceRow,
    channel: NotificationChannelType,
    enabled: boolean,
    jobId: string | null,
  ) => {
    const key = `${row.event.type}|${channel}|${jobId ?? ""}`;
    setBusy((b) => new Set(b).add(key));
    try {
      const r = await setPref({ variables: { event: row.event.type, channel, enabled, jobId } });
      const out = r.data?.setNotificationPreference;
      if (out) {
        const row2 = unmaskRow(out);
        update((base) => replaceRow(base, row2));
      }
    } catch (err) {
      toast.error(userMessage(err));
    } finally {
      setBusy((b) => {
        const n = new Set(b);
        n.delete(key);
        return n;
      });
    }
  };

  const doReset = async (jobId: string | null) => {
    try {
      const r = await reset({ variables: { jobId } });
      const out = r.data?.resetNotificationPreferences;
      if (out) {
        const next = unmaskPrefs(out);
        update(() => next);
      }
    } catch (err) {
      toast.error(userMessage(err));
      throw err;
    }
  };

  let matrix;
  if (!prefs) {
    matrix = error ? (
      <div className="flex items-center justify-between gap-3 rounded-md border border-destructive/40 px-4 py-3 text-sm">
        <span>{M.prefs.loadFailed}</span>
        <Button
          size="sm"
          variant="outline"
          onClick={() => {
            void refetch().catch(() => undefined);
          }}
        >
          {M.verify.retry}
        </Button>
      </div>
    ) : loading ? (
      <Skeleton className="h-64" />
    ) : null;
  } else {
    const groups = [...prefs.groups].sort(
      (a, b) => GROUP_ORDER.indexOf(a.group) - GROUP_ORDER.indexOf(b.group),
    );
    matrix = (
      <div className="flex flex-col gap-8">
        {!prefs.emailVerified || prefs.activeWebhooks === 0 ? (
          <ul className="flex flex-col gap-1.5">
            {!prefs.emailVerified ? (
              <li className="flex gap-2 text-xs text-status-partial-fg">
                <Info aria-hidden="true" className="mt-px size-3.5 shrink-0" />
                {M.prefs.emailUnverified}
              </li>
            ) : null}
            {prefs.activeWebhooks === 0 ? (
              <li className="flex gap-2 text-xs text-muted-foreground">
                <Info aria-hidden="true" className="mt-px size-3.5 shrink-0" />
                {M.prefs.noWebhooks}
              </li>
            ) : null}
          </ul>
        ) : null}
        {groups.map((g) => (
          <section
            key={g.group}
            aria-label={g.label || M.groups[g.group]}
            className="flex flex-col gap-2"
          >
            <h3 className="flex items-baseline gap-3 border-b border-rule pb-1.5">
              <span className="font-display text-xl leading-none tracking-tight">
                {g.label || M.groups[g.group]}
              </span>
              <span className="font-mono text-2xs text-muted-foreground tabular-nums">
                {String(g.events.length).padStart(2, "0")}
              </span>
            </h3>
            <GroupMatrix
              label={g.label || M.groups[g.group]}
              rows={g.events}
              security={g.group === "SECURITY"}
              busy={busy}
              onToggle={(row, channel, v) => void toggle(row, channel, v, null)}
            />
          </section>
        ))}
      </div>
    );
  }

  return (
    <div className="flex flex-col">
      <ChannelsSection />
      <section
        aria-labelledby="notification-prefs-heading"
        className="flex flex-col gap-5 border-b border-rule py-8"
      >
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="flex max-w-prose flex-col gap-2">
            <div className="flex items-center gap-2.5">
              <span
                aria-hidden="true"
                className="inline-flex size-7 items-center justify-center rounded-sm border bg-surface text-muted-foreground"
              >
                <BellRing className="size-3.5" />
              </span>
              <h2
                id="notification-prefs-heading"
                className="text-[0.9375rem] font-semibold tracking-tight"
              >
                {M.prefs.heading}
              </h2>
            </div>
            <p className="text-sm text-muted-foreground">{M.prefs.lead}</p>
          </div>
          <Button
            variant="outline"
            size="sm"
            disabled={!prefs}
            onClick={() => {
              setConfirmReset(true);
            }}
          >
            <RotateCcw aria-hidden="true" /> {M.prefs.resetAll}
          </Button>
        </div>
        {matrix}
      </section>
      {prefs ? (
        <OverridesSection
          prefs={prefs}
          busy={busy}
          onToggle={(row, channel, v, jobId) => void toggle(row, channel, v, jobId)}
          onReset={async (job) => {
            await doReset(job.jobId);
            toast.success(M.overrides.resetDone(job.jobName));
          }}
        />
      ) : null}
      <ConfirmDialog
        open={confirmReset}
        onOpenChange={setConfirmReset}
        title={M.prefs.resetAllTitle}
        description={M.prefs.resetAllBody}
        confirmLabel={M.prefs.resetAll}
        destructive
        onConfirm={async () => {
          await doReset(null);
          toast.success(M.prefs.resetDone);
        }}
      />
    </div>
  );
}
