import { useMutation, useQuery } from "@apollo/client/react";
import { CalendarClock, Info, Loader2, Send } from "lucide-react";
import { useId, useMemo, useState, type ReactNode } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";
import { Skeleton } from "@/components/ui/skeleton";
import { Section, StatePill } from "@/features/account/section";
import { timezones } from "@/features/jobs/schedule";
import { getFragmentData } from "@/graphql/gen";
import { userMessage } from "@/lib/api-error";
import { formatZonedDateTime } from "@/lib/format";
import { cn } from "@/lib/utils";

import { notificationsMessages as M } from "./messages";
import {
  MySummarySettingsQuery,
  SendSummaryPreviewMutation,
  SetSummarySettingsMutation,
  SummarySettingsFields,
} from "./operations";
import {
  FREQUENCIES,
  HOURS,
  MONTH_DAYS,
  draftFrom,
  hourLabel,
  isDirty,
  toInput,
  type SummaryDraft,
  type SummaryView,
} from "./summary-model";

const T = M.summary;

function Field({
  label,
  hint,
  className,
  children,
}: {
  label: string;
  hint?: string;
  className?: string;
  children: (ids: { id: string; describedBy: string | undefined }) => ReactNode;
}) {
  const id = useId();
  const hintId = `${id}-hint`;
  return (
    <div className={cn("flex flex-col gap-1.5", className)}>
      <label htmlFor={id} className="eyebrow">
        {label}
      </label>
      {children({ id, describedBy: hint ? hintId : undefined })}
      {hint ? (
        <p id={hintId} className="text-xs text-muted-foreground">
          {hint}
        </p>
      ) : null}
    </div>
  );
}

function describe(s: SummaryView["defaults"]): string {
  const f = T.frequencies[s.frequency].label;
  if (s.frequency === "NEVER") return f;
  let at = hourLabel(s.hour);
  if (s.frequency === "WEEKLY") at = `${T.weekdays[s.weekday - 1] ?? ""} ${at}`;
  if (s.frequency === "MONTHLY") at = `${T.monthDayOption(s.monthDay).toLowerCase()}, ${at}`;
  return T.describe(f, at, s.timezone);
}

function Editor({ view }: { view: SummaryView }) {
  const groupId = useId();
  const zones = useMemo(() => timezones(), []);
  const [draft, setDraft] = useState<SummaryDraft>(() => draftFrom(view));
  const [save, { loading: saving }] = useMutation(SetSummarySettingsMutation, {
    fetchPolicy: "no-cache",
    update(cache, { data }) {
      if (data) {
        cache.writeQuery({
          query: MySummarySettingsQuery,
          data: { mySummarySettings: data.setSummarySettings },
        });
      }
    },
  });
  const [preview, { loading: previewing }] = useMutation(SendSummaryPreviewMutation, {
    fetchPolicy: "no-cache",
  });
  const set = (patch: Partial<SummaryDraft>) => {
    setDraft((d) => ({ ...d, ...patch }));
  };
  const dirty = isDirty(view, draft);
  const off = draft.frequency === "NEVER";
  const zone = view.settings.timezone;

  const onSave = async () => {
    try {
      await save({ variables: { input: toInput(draft) } });
      toast.success(T.saved);
    } catch (err) {
      toast.error(userMessage(err));
    }
  };
  const onPreview = async () => {
    try {
      const r = await preview();
      if (r.data) toast.success(T.previewSent(r.data.sendSummaryPreview.emailVerified));
    } catch (err) {
      toast.error(userMessage(err));
    }
  };

  let next: ReactNode;
  if (view.settings.frequency === "NEVER" || !view.nextSummaryAt) {
    next = <span className="text-muted-foreground">{T.nextNever}</span>;
  } else {
    next = (
      <span>
        <time dateTime={view.nextSummaryAt} className="font-mono tabular-nums">
          {formatZonedDateTime(view.nextSummaryAt, zone)}
        </time>{" "}
        <span className="text-muted-foreground">({zone})</span>
      </span>
    );
  }
  let last: string | null = null;
  if (view.lastOutcome === "no_jobs" && view.lastOutcomeAt) {
    last = T.lastNoJobs(formatZonedDateTime(view.lastOutcomeAt, zone));
  } else if (view.lastSentAt) {
    last = T.last(formatZonedDateTime(view.lastSentAt, zone));
  }

  const hints: string[] = [];
  if (!view.custom) hints.push(T.defaults(describe(view.defaults)));
  if (!view.emailVerified) hints.push(T.emailUnverified);
  if (view.visibleJobs === 0) hints.push(T.noJobs);

  return (
    <div className="flex flex-col gap-5">
      <fieldset className="flex flex-col gap-2">
        <legend className="eyebrow mb-1.5">{T.frequency}</legend>
        <div role="radiogroup" aria-label={T.frequency} className="grid gap-2 @lg:grid-cols-5">
          {FREQUENCIES.map((f) => {
            const checked = draft.frequency === f;
            return (
              <label
                key={f}
                className={cn(
                  "relative flex cursor-pointer flex-col gap-0.5 rounded-md border bg-surface px-3 py-2.5 transition-colors",
                  "has-[:focus-visible]:ring-[3px] has-[:focus-visible]:ring-ring/50",
                  checked
                    ? "border-primary/60 bg-accent text-accent-foreground"
                    : "hover:border-border-strong",
                )}
              >
                <input
                  type="radio"
                  name={`${groupId}-frequency`}
                  value={f}
                  checked={checked}
                  onChange={() => {
                    set({ frequency: f });
                  }}
                  className="sr-only"
                />
                <span className="text-sm font-medium">{T.frequencies[f].label}</span>
                <span className="text-xs text-muted-foreground">{T.frequencies[f].hint}</span>
              </label>
            );
          })}
        </div>
      </fieldset>

      {off ? null : (
        <div className="grid gap-4 @lg:grid-cols-[7rem_minmax(0,11rem)_minmax(0,1fr)]">
          <Field label={T.hour}>
            {({ id }) => (
              <NativeSelect
                id={id}
                value={String(draft.hour)}
                onChange={(e) => {
                  set({ hour: Number(e.target.value) });
                }}
                className="font-mono"
              >
                {HOURS.map((h) => (
                  <option key={h} value={h}>
                    {hourLabel(h)}
                  </option>
                ))}
              </NativeSelect>
            )}
          </Field>
          {draft.frequency === "WEEKLY" ? (
            <Field label={T.weekday}>
              {({ id }) => (
                <NativeSelect
                  id={id}
                  value={String(draft.weekday)}
                  onChange={(e) => {
                    set({ weekday: Number(e.target.value) });
                  }}
                >
                  {T.weekdays.map((name, i) => (
                    <option key={name} value={i + 1}>
                      {name}
                    </option>
                  ))}
                </NativeSelect>
              )}
            </Field>
          ) : draft.frequency === "MONTHLY" ? (
            <Field label={T.monthDay}>
              {({ id }) => (
                <NativeSelect
                  id={id}
                  value={String(draft.monthDay)}
                  onChange={(e) => {
                    set({ monthDay: Number(e.target.value) });
                  }}
                >
                  {MONTH_DAYS.map((d) => (
                    <option key={d} value={d}>
                      {T.monthDayOption(d)}
                    </option>
                  ))}
                </NativeSelect>
              )}
            </Field>
          ) : (
            <div aria-hidden="true" className="hidden @lg:block" />
          )}
          <Field label={T.timezone} hint={T.timezoneHint}>
            {({ id, describedBy }) => (
              <>
                <Input
                  id={id}
                  aria-describedby={describedBy}
                  list={`${groupId}-zones`}
                  value={draft.timezone}
                  spellCheck={false}
                  autoComplete="off"
                  onChange={(e) => {
                    set({ timezone: e.target.value });
                  }}
                  className="font-mono"
                />
                <datalist id={`${groupId}-zones`}>
                  {zones.map((z) => (
                    <option key={z} value={z} />
                  ))}
                </datalist>
              </>
            )}
          </Field>
        </div>
      )}

      <div className="flex flex-col gap-1 border-y border-rule py-3" aria-live="polite">
        <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1 text-sm">
          <span className="eyebrow">{T.next}</span>
          {next}
        </div>
        {dirty && view.custom ? (
          <p className="text-xs text-muted-foreground">{T.nextAfterSave}</p>
        ) : null}
        {last ? <p className="font-mono text-2xs text-muted-foreground">{last}</p> : null}
      </div>

      {hints.length > 0 ? (
        <ul className="flex flex-col gap-1.5">
          {hints.map((h) => (
            <li
              key={h}
              className={cn(
                "flex gap-2 text-xs",
                h === T.emailUnverified ? "text-status-partial-fg" : "text-muted-foreground",
              )}
            >
              <Info aria-hidden="true" className="mt-px size-3.5 shrink-0" />
              {h}
            </li>
          ))}
        </ul>
      ) : null}

      <div className="flex flex-wrap items-center gap-2">
        <Button disabled={!dirty || saving} onClick={() => void onSave()}>
          {saving ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
          {T.save}
        </Button>
        <Button
          variant="outline"
          disabled={previewing || view.visibleJobs === 0}
          onClick={() => void onPreview()}
        >
          {previewing ? (
            <Loader2 aria-hidden="true" className="animate-spin" />
          ) : (
            <Send aria-hidden="true" />
          )}
          {T.preview}
        </Button>
      </div>
    </div>
  );
}

/**
 * Profile → Notifications: the periodic summary e-mail (D-226). The
 * frequency and the local time are the user's own; the zone starts as the
 * browser's. The server computes the next due time.
 */
export function SummarySection() {
  const { data, error, loading, refetch } = useQuery(MySummarySettingsQuery, {
    fetchPolicy: "cache-and-network",
  });
  const view = data ? getFragmentData(SummarySettingsFields, data.mySummarySettings) : null;
  const status = view ? (
    <StatePill on={view.settings.frequency !== "NEVER"}>
      {view.settings.frequency === "NEVER" ? T.off : T.frequencies[view.settings.frequency].label}
    </StatePill>
  ) : null;
  let body: ReactNode;
  if (view) {
    // Remount the editor when the stored settings change (a save).
    const key = `${view.settings.frequency}|${String(view.settings.hour)}|${String(view.settings.weekday)}|${String(view.settings.monthDay)}|${view.settings.timezone}|${String(view.custom)}`;
    body = <Editor key={key} view={view} />;
  } else if (error) {
    body = (
      <div className="flex items-center justify-between gap-3 rounded-md border border-destructive/40 px-4 py-3 text-sm">
        <span>{T.loadFailed}</span>
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
    );
  } else {
    body = loading ? <Skeleton className="h-48" /> : null;
  }
  return (
    <Section icon={CalendarClock} title={T.heading} lead={T.lead} status={status}>
      {body}
    </Section>
  );
}
