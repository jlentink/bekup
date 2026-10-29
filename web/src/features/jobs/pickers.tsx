import { useMutation } from "@apollo/client/react";
import { ArrowDown, ArrowUp, Database, Loader2, Plus, RefreshCw, X } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { DestinationType, JobDbMode } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { ListDatabasesMutation } from "@/lib/operations";
import { cn } from "@/lib/utils";

import { NativeSelect } from "./fields";

const T = {
  load: "Load databases from the server",
  reload: "Reload",
  loading: "Asking the server",
  excludeLegend: "Exclude these databases",
  excludeHint: "Every other database is backed up, including ones created later.",
  selectLegend: "Back up these databases",
  selectHint: "Only the checked databases are backed up.",
  notReported: "not reported by the server",
  addName: "Add a database by name",
  add: "Add",
  noneLoaded: "Load the list from the server or add names by hand.",
  noConnection: "Choose a connection first.",
  destinations: "Destinations in fan-out order",
  addDestination: "Add a destination",
  chooseDestination: "Choose a destination",
  up: (n: string) => `Move ${n} up`,
  down: (n: string) => `Move ${n} down`,
  remove: (n: string) => `Remove ${n}`,
  notReady: "not ready",
  noDestinations: "No destination chosen yet.",
} as const;

/**
 * Database selection of a job: in mode ALL the checked names are excluded,
 * in mode SELECTED they are the ones backed up. The names come from
 * `listDatabases` (rate limited, staff only) or are typed in.
 */
export function DatabasePicker({
  connectionId,
  mode,
  value,
  onChange,
}: {
  connectionId: string;
  mode: JobDbMode;
  value: readonly string[];
  onChange: (next: string[]) => void;
}) {
  const [listDatabases, { loading }] = useMutation(ListDatabasesMutation);
  const [loaded, setLoaded] = useState<{
    connectionId: string;
    names: string[];
    warnings: string[];
  } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [extra, setExtra] = useState("");
  const names = loaded?.connectionId === connectionId ? loaded.names : [];
  const all = [...names, ...value.filter((v) => !names.includes(v))];
  const selected = new Set(value);

  const load = async () => {
    setError(null);
    try {
      const r = await listDatabases({ variables: { id: connectionId } });
      const d = r.data?.listDatabases;
      if (d) setLoaded({ connectionId, names: d.databases, warnings: d.warnings });
    } catch (err) {
      setError(userMessage(err));
    }
  };
  const toggle = (name: string, on: boolean) => {
    onChange(on ? [...value, name] : value.filter((v) => v !== name));
  };
  const addExtra = () => {
    const n = extra.trim();
    if (n && !selected.has(n)) onChange([...value, n]);
    setExtra("");
  };
  const legend = mode === "ALL" ? T.excludeLegend : T.selectLegend;

  return (
    <fieldset className="flex flex-col gap-3 rounded-md border bg-surface px-4 py-3.5">
      <legend className="px-1 text-sm font-medium">{legend}</legend>
      <p className="-mt-1 text-xs text-muted-foreground">
        {mode === "ALL" ? T.excludeHint : T.selectHint}
      </p>
      <div className="flex flex-wrap items-center gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={!connectionId || loading}
          onClick={() => {
            void load();
          }}
        >
          {loading ? (
            <Loader2 aria-hidden="true" className="animate-spin" />
          ) : names.length ? (
            <RefreshCw aria-hidden="true" />
          ) : (
            <Database aria-hidden="true" />
          )}
          {loading ? T.loading : names.length ? T.reload : T.load}
        </Button>
        {!connectionId ? (
          <span className="text-xs text-muted-foreground">{T.noConnection}</span>
        ) : null}
      </div>
      {error ? (
        <p role="alert" className="text-xs text-destructive">
          {error}
        </p>
      ) : null}
      {loaded?.connectionId === connectionId && loaded.warnings.length > 0 ? (
        <ul className="text-xs text-status-partial-fg">
          {loaded.warnings.map((w) => (
            <li key={w}>{w}</li>
          ))}
        </ul>
      ) : null}
      {all.length === 0 ? (
        <p className="text-xs text-muted-foreground">{T.noneLoaded}</p>
      ) : (
        <ul className="grid gap-x-6 gap-y-1 sm:grid-cols-2" aria-label={legend}>
          {all.map((name) => (
            <li key={name}>
              <label className="flex items-center gap-2 py-0.5 font-mono text-xs">
                <input
                  type="checkbox"
                  className="size-4 accent-primary"
                  checked={selected.has(name)}
                  onChange={(e) => {
                    toggle(name, e.target.checked);
                  }}
                />
                <span className="truncate">{name}</span>
                {names.length > 0 && !names.includes(name) ? (
                  <span className="font-sans text-2xs text-muted-foreground">
                    ({T.notReported})
                  </span>
                ) : null}
              </label>
            </li>
          ))}
        </ul>
      )}
      <div className="flex gap-2">
        <Input
          aria-label={T.addName}
          placeholder={T.addName}
          value={extra}
          spellCheck={false}
          autoComplete="off"
          onChange={(e) => {
            setExtra(e.target.value);
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              addExtra();
            }
          }}
          className="h-8 font-mono text-xs"
        />
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={extra.trim() === ""}
          onClick={addExtra}
        >
          <Plus aria-hidden="true" /> {T.add}
        </Button>
      </div>
    </fieldset>
  );
}

export interface DestinationOption {
  id: string;
  name: string;
  type: DestinationType;
  ready: boolean;
}

/** Ordered multi-select of destinations (1-16; uploads fan out in this order). */
export function DestinationPicker({
  options,
  value,
  onChange,
  invalid,
}: {
  options: readonly DestinationOption[];
  value: readonly string[];
  onChange: (next: string[]) => void;
  invalid?: boolean;
}) {
  const byId = new Map(options.map((o) => [o.id, o]));
  const remaining = options.filter((o) => !value.includes(o.id));
  const move = (i: number, d: -1 | 1) => {
    const next = [...value];
    const j = i + d;
    const a = next[i];
    const b = next[j];
    if (a === undefined || b === undefined) return;
    next[i] = b;
    next[j] = a;
    onChange(next);
  };
  return (
    <div className="flex flex-col gap-3">
      {value.length === 0 ? (
        <p className={cn("text-sm", invalid ? "text-destructive" : "text-muted-foreground")}>
          {T.noDestinations}
        </p>
      ) : (
        <ol
          aria-label={T.destinations}
          className="flex flex-col divide-y divide-rule rounded-md border"
        >
          {value.map((id, i) => {
            const d = byId.get(id);
            const name = d?.name ?? id;
            return (
              <li key={id} className="flex items-center gap-3 px-3 py-1.5">
                <span aria-hidden="true" className="w-5 font-mono text-xs text-muted-foreground">
                  {String(i + 1).padStart(2, "0")}
                </span>
                <span className="min-w-0 flex-1 truncate text-sm">{name}</span>
                {d ? (
                  <span className="font-mono text-2xs tracking-wide text-muted-foreground uppercase">
                    {d.type}
                    {d.ready ? "" : ` · ${T.notReady}`}
                  </span>
                ) : null}
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-xs"
                  aria-label={T.up(name)}
                  disabled={i === 0}
                  onClick={() => {
                    move(i, -1);
                  }}
                >
                  <ArrowUp aria-hidden="true" />
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-xs"
                  aria-label={T.down(name)}
                  disabled={i === value.length - 1}
                  onClick={() => {
                    move(i, 1);
                  }}
                >
                  <ArrowDown aria-hidden="true" />
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-xs"
                  aria-label={T.remove(name)}
                  onClick={() => {
                    onChange(value.filter((v) => v !== id));
                  }}
                >
                  <X aria-hidden="true" />
                </Button>
              </li>
            );
          })}
        </ol>
      )}
      {remaining.length > 0 && value.length < 16 ? (
        <NativeSelect
          aria-label={T.addDestination}
          value=""
          onChange={(e) => {
            if (e.target.value) onChange([...value, e.target.value]);
          }}
          className="sm:max-w-sm"
        >
          <option value="">{T.chooseDestination}</option>
          {remaining.map((o) => (
            <option key={o.id} value={o.id}>
              {o.name} ({o.type.toLowerCase()}
              {o.ready ? "" : `, ${T.notReady}`})
            </option>
          ))}
        </NativeSelect>
      ) : null}
    </div>
  );
}
