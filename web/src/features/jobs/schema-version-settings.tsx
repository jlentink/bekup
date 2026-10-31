import { useMutation, useQuery } from "@apollo/client/react";
import { Loader2, ShieldAlert } from "lucide-react";
import { useId, useState } from "react";
import { toast } from "sonner";

import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { isStepUpCancelled, useStepUp } from "@/features/auth/use-step-up";
import type { JobDetailQuery as JobDetailResult, SchemaVersionMode } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { cn } from "@/lib/utils";

import { Field, NativeSelect } from "./fields";
import { SchemaVersionPresetsQuery, SetJobSchemaVersionMutation } from "./operations";
import {
  MAX_CUSTOM_SQL_BYTES,
  REFUSED_SUMMARY,
  SCHEMA_RE,
  customSqlProblem,
  utf8Bytes,
} from "./schema-version-model";

type Job = NonNullable<JobDetailResult["job"]>;

const T = {
  legend: "Capture",
  modes: {
    NONE: { label: "Off", hint: "No version is read." },
    PRESET: { label: "Framework preset", hint: "Reviewed SQL for a migration tool." },
    CUSTOM: { label: "Custom query", hint: "Your own SELECT statement." },
  } satisfies Record<SchemaVersionMode, { label: string; hint: string }>,
  preset: "Migration framework",
  choosePreset: "Choose a framework",
  presetRequired: "Choose a framework.",
  reads: (table: string) => `Reads ${table}`,
  schema: "Schema",
  schemaHint: "The PostgreSQL schema of the framework's table.",
  schemaInvalid: "A letter or underscore, then up to 62 letters, digits or underscores.",
  customSql: "Version query",
  customHint: (n: number) =>
    `${String(n)} of ${String(MAX_CUSTOM_SQL_BYTES)} bytes. One SELECT or WITH statement without ; or comments; the first column of the first row becomes the version.`,
  warningTitle: "This query runs on your databases",
  warning: [
    "It runs on every backed-up database with the backup role's rights, read-only, inside the dump's snapshot, stopped after 5 seconds.",
    "PostgreSQL resolves names only in pg_catalog: qualify tables with their schema (public.schema_migrations).",
    "Saving needs a fresh confirmation of your identity; the change is audited and admins are alerted.",
  ],
  refusedTitle: "What the query may not contain",
  refused: [
    "It must start with SELECT or WITH. Every rule applies to the whole text, also inside quotes.",
    `Characters: ${REFUSED_SUMMARY.characters}.`,
    `Words: ${REFUSED_SUMMARY.words}.`,
    `Functions: ${REFUSED_SUMMARY.functions}.`,
    "This list is a guardrail, not the protection: the query runs read-only with time limits and only a short result is kept. Give the backup role only the rights it needs.",
  ],
  notes:
    "A failed capture is a warning on the run (the backup itself is unaffected). Versions are at most 64 printable characters.",
  save: "Save schema version",
  saved: "Schema-version capture saved.",
} as const;

const MODES: readonly SchemaVersionMode[] = ["NONE", "PRESET", "CUSTOM"];

/**
 * The job's schema-version capture (D-210): off, a framework preset (the
 * server's list) or a custom query. A custom query needs a fresh step-up;
 * the server validates and audits every change.
 */
export function SchemaVersionSettings({ job }: { job: Job }) {
  const sv = job.schemaVersion;
  const { withStepUp } = useStepUp();
  const presets = useQuery(SchemaVersionPresetsQuery, { fetchPolicy: "cache-first" });
  const [save, saveState] = useMutation(SetJobSchemaVersionMutation);
  const [mode, setMode] = useState<SchemaVersionMode>(sv.mode);
  const [preset, setPreset] = useState(sv.preset ?? "");
  const [schema, setSchema] = useState(sv.schema || "public");
  const [sql, setSql] = useState(sv.customSql ?? "");
  const [touched, setTouched] = useState(false);
  const [error, setError] = useState<string>();
  const groupId = useId();

  const engine = job.connection.engine;
  const isPG = engine === "POSTGRES";
  const list = presets.data?.schemaVersionPresets ?? [];
  const chosen = list.find((p) => p.name === preset);
  const presetMissing = mode === "PRESET" && preset === "";
  const schemaBad = mode === "PRESET" && isPG && !SCHEMA_RE.test(schema);
  const sqlProblem = mode === "CUSTOM" ? customSqlProblem(sql) : null;
  const canSubmit = !saveState.loading && !presetMissing && !schemaBad && sqlProblem === null;

  const submit = async () => {
    setTouched(true);
    setError(undefined);
    if (!canSubmit) return;
    const input =
      mode === "PRESET"
        ? { mode, preset, ...(isPG ? { schema } : {}) }
        : mode === "CUSTOM"
          ? { mode, customSql: sql }
          : { mode };
    try {
      const run = () => save({ variables: { jobId: job.id, input } });
      await (mode === "CUSTOM" ? withStepUp(run) : run());
      toast.success(T.saved);
    } catch (err) {
      if (isStepUpCancelled(err)) return;
      setError(userMessage(err));
    }
  };

  return (
    <form
      className="flex flex-col gap-5"
      aria-label="Schema-version settings"
      onSubmit={(e) => {
        e.preventDefault();
        void submit();
      }}
    >
      <fieldset className="flex flex-col gap-2">
        <legend className="mb-2 text-sm font-medium">{T.legend}</legend>
        <div className="grid gap-2 sm:grid-cols-3" role="presentation">
          {MODES.map((m) => {
            const id = `${groupId}-${m}`;
            return (
              <label
                key={m}
                htmlFor={id}
                className={cn(
                  "grid cursor-pointer grid-cols-[auto_minmax(0,1fr)] items-start gap-x-2.5 gap-y-0.5 rounded-md border bg-surface px-3 py-2.5 transition-colors",
                  "has-[:focus-visible]:ring-[3px] has-[:focus-visible]:ring-ring/50",
                  mode === m ? "border-primary/60 bg-accent" : "hover:bg-surface-sunken",
                )}
              >
                <input
                  id={id}
                  type="radio"
                  name={`${groupId}-mode`}
                  value={m}
                  checked={mode === m}
                  onChange={() => {
                    setMode(m);
                    setError(undefined);
                  }}
                  className="row-span-2 mt-0.5 size-4 shrink-0 accent-primary"
                />
                <span className="text-sm leading-tight font-medium">{T.modes[m].label}</span>
                <span className="text-xs text-muted-foreground">{T.modes[m].hint}</span>
              </label>
            );
          })}
        </div>
      </fieldset>

      {mode === "PRESET" ? (
        <div className="grid gap-4 sm:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
          <Field
            label={T.preset}
            hint={
              chosen ? (
                <>
                  {chosen.description}{" "}
                  <span className="font-mono">
                    {T.reads(isPG ? chosen.postgresTable : chosen.mysqlTable)}
                  </span>
                </>
              ) : undefined
            }
            error={touched && presetMissing ? T.presetRequired : null}
          >
            {({ id, describedBy, invalid }) => (
              <NativeSelect
                id={id}
                aria-describedby={describedBy}
                aria-invalid={invalid || undefined}
                value={preset}
                onChange={(e) => {
                  setPreset(e.target.value);
                }}
              >
                <option value="">{T.choosePreset}</option>
                {list.map((p) => (
                  <option key={p.name} value={p.name}>
                    {p.label}
                  </option>
                ))}
              </NativeSelect>
            )}
          </Field>
          {isPG ? (
            <Field label={T.schema} hint={T.schemaHint} error={schemaBad ? T.schemaInvalid : null}>
              {({ id, describedBy, invalid }) => (
                <Input
                  id={id}
                  aria-describedby={describedBy}
                  aria-invalid={invalid || undefined}
                  className="font-mono"
                  spellCheck={false}
                  value={schema}
                  onChange={(e) => {
                    setSchema(e.target.value);
                  }}
                />
              )}
            </Field>
          ) : null}
        </div>
      ) : null}

      {mode === "CUSTOM" ? (
        <div className="flex flex-col gap-3">
          <div
            role="note"
            className="flex flex-col gap-1.5 rounded-md border border-l-2 border-l-status-partial bg-surface px-3 py-2.5"
          >
            <p className="flex items-center gap-2 text-sm font-medium text-status-partial-fg">
              <ShieldAlert aria-hidden="true" className="size-4 shrink-0" />
              {T.warningTitle}
            </p>
            <ul className="flex list-disc flex-col gap-0.5 pl-9 text-xs text-muted-foreground">
              {T.warning.map((w) => (
                <li key={w}>{w}</li>
              ))}
            </ul>
            <details className="pl-6 text-xs text-muted-foreground">
              <summary className="cursor-pointer font-medium text-foreground">
                {T.refusedTitle}
              </summary>
              <ul className="mt-1 flex list-disc flex-col gap-0.5 pl-5">
                {T.refused.map((r) => (
                  <li key={r} className="break-words">
                    {r}
                  </li>
                ))}
              </ul>
            </details>
          </div>
          <Field
            label={T.customSql}
            hint={T.customHint(utf8Bytes(sql))}
            error={touched || sql !== "" ? sqlProblem : null}
          >
            {({ id, describedBy, invalid }) => (
              <textarea
                id={id}
                aria-describedby={describedBy}
                aria-invalid={invalid || undefined}
                value={sql}
                rows={5}
                spellCheck={false}
                placeholder={
                  isPG
                    ? "SELECT max(version)::text FROM public.schema_migrations"
                    : "SELECT MAX(version) FROM schema_migrations"
                }
                onChange={(e) => {
                  setSql(e.target.value);
                }}
                className="w-full rounded-md border border-input bg-surface px-3 py-2 font-mono text-xs shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 aria-invalid:border-destructive dark:bg-surface-sunken"
              />
            )}
          </Field>
        </div>
      ) : null}

      <p className="text-xs text-muted-foreground">{T.notes}</p>
      {error ? (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      ) : null}
      <Button type="submit" variant="outline" className="self-start" disabled={saveState.loading}>
        {saveState.loading ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
        {T.save}
      </Button>
    </form>
  );
}
