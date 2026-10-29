import { skipToken, useQuery } from "@apollo/client/react";
import { Search, TableProperties } from "lucide-react";
import { useDeferredValue, useId, useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import type { RowCountKind, RunItemStatsQuery as StatsResult } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { formatDateTime } from "@/lib/format";
import { cn } from "@/lib/utils";

import { codeTitle } from "./codes";
import { RunItemStatsQuery } from "./operations";

type Item = NonNullable<StatsResult["run"]>["items"][number];
export type ItemStats = NonNullable<Item["stats"]>;
type Table = ItemStats["tables"][number];
type Finding = ItemStats["warnings"][number];

/** Table rows rendered before "Show all" (a database may list 10 000). */
export const TABLE_PREVIEW = 50;

const T = {
  heading: "Per-database statistics",
  lead: "The source statistics taken for each verified dump: names and counts only, never data.",
  none: "No statistics were recorded for this run (failed or unverified dumps, or statistics unavailable).",
  pending: "Statistics appear once the run has finished.",
  level: { STANDARD: "standard", DEEP: "deep" },
  consistency: {
    SNAPSHOT: "in the dump's snapshot",
    APPROXIMATE: "taken before the dump",
  },
  consistencyHint: {
    SNAPSHOT: "Taken inside the snapshot the dump used: exact counts match the dump.",
    APPROXIMATE: "Taken right before the dump: rows may have changed since.",
  },
  globals: "Cluster globals",
  objects: "Objects",
  tables: "Tables",
  tablesOf: (shown: number, total: number) =>
    shown < total ? `${String(shown)} of ${String(total)} listed` : String(total),
  truncated: (listed: number, total: number) =>
    `Only ${String(listed)} of ${String(total)} tables are listed: the statistics table limit was reached. The remaining tables were dumped but not counted individually.`,
  warnings: "Completeness warnings",
  notices: "Notices",
  more: "and more",
  filter: "Filter tables",
  noMatch: "No table matches.",
  showAll: (n: number) => `Show all ${String(n)} tables`,
  cols: { name: "Table", rows: "Rows", count: "Count" },
  summary: (e: number, s: number, u: number) =>
    [
      e > 0 ? `${String(e)} exact` : "",
      s > 0 ? `${String(s)} estimated` : "",
      u > 0 ? `${String(u)} unknown` : "",
    ]
      .filter(Boolean)
      .join(" · "),
  recorded: (at: string) => `recorded ${at}`,
} as const;

const numberFormat = new Intl.NumberFormat();

const COUNT_META: Record<RowCountKind, { label: string; title: string; className: string }> = {
  EXACT: {
    label: "exact",
    title: "Counted exactly.",
    className: "border-status-success/40 bg-status-success-soft text-status-success-fg",
  },
  ESTIMATE: {
    label: "estimate",
    title: "A planner or information_schema estimate.",
    className: "border-border bg-surface-sunken text-muted-foreground",
  },
  UNKNOWN: {
    label: "unknown",
    title: "Not counted (time limit, budget or failure).",
    className: "border-status-partial/45 bg-status-partial-soft text-status-partial-fg",
  },
};

/** The exactness of one table's row count, plus "approx." outside the snapshot. */
export function CountBadges({ table }: { table: Table }) {
  const meta = COUNT_META[table.count];
  return (
    <span className="inline-flex flex-wrap items-center justify-end gap-1">
      <span
        data-count={table.count}
        title={meta.title}
        className={cn(
          "inline-flex h-5 items-center rounded-sm border px-1.5 font-mono text-2xs",
          meta.className,
        )}
      >
        {meta.label}
      </span>
      {table.approximate ? (
        <span
          title="Taken outside the dump's snapshot (MySQL/MariaDB): rows may have changed."
          className="inline-flex h-5 items-center rounded-sm border border-dashed border-border px-1.5 font-mono text-2xs text-muted-foreground"
        >
          approximate
        </span>
      ) : null}
    </span>
  );
}

function FindingList({
  findings,
  kind,
}: {
  findings: readonly Finding[];
  kind: "warning" | "notice";
}) {
  if (findings.length === 0) return null;
  const warning = kind === "warning";
  return (
    <div className="flex flex-col gap-1.5">
      <h5 className="eyebrow">{warning ? T.warnings : T.notices}</h5>
      <ul className="flex flex-col gap-1.5">
        {findings.map((f) => (
          <li
            key={f.code}
            data-kind={kind}
            className={cn(
              "rounded-md border px-3 py-2",
              warning
                ? "border-l-4 border-status-partial/40 border-l-status-partial bg-status-partial-soft/50"
                : "border-dashed bg-surface",
            )}
          >
            <p
              className={cn(
                "flex flex-wrap items-baseline gap-x-2 text-sm",
                warning ? "font-medium text-status-partial-fg" : "text-foreground",
              )}
            >
              {codeTitle(f.code)}
              <span className="font-mono text-2xs font-normal text-muted-foreground">
                {f.code} · {numberFormat.format(f.count)}
              </span>
            </p>
            {f.objects.length > 0 ? (
              <ul className="mt-1.5 flex flex-wrap gap-1">
                {f.objects.map((o) => (
                  <li
                    key={o}
                    className="rounded-sm border bg-background/60 px-1.5 py-0.5 font-mono text-2xs break-all"
                  >
                    {o}
                  </li>
                ))}
                {f.truncated ? (
                  <li className="px-1 py-0.5 text-2xs text-muted-foreground">{T.more}</li>
                ) : null}
              </ul>
            ) : null}
          </li>
        ))}
      </ul>
    </div>
  );
}

function TableList({ stats }: { stats: ItemStats }) {
  const filterId = useId();
  const [filter, setFilter] = useState("");
  const [all, setAll] = useState(false);
  const q = useDeferredValue(filter.trim().toLowerCase());
  const matching = q ? stats.tables.filter((t) => t.name.toLowerCase().includes(q)) : stats.tables;
  const shown = all ? matching : matching.slice(0, TABLE_PREVIEW);
  if (stats.tables.length === 0) return null;
  return (
    <div className="flex flex-col gap-2">
      {stats.tables.length > 10 ? (
        <div className="relative max-w-xs">
          <label htmlFor={filterId} className="sr-only">
            {T.filter}
          </label>
          <Search
            aria-hidden="true"
            className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground"
          />
          <Input
            id={filterId}
            type="search"
            className="h-8 pl-8 text-xs"
            placeholder={T.filter}
            value={filter}
            onChange={(e) => {
              setFilter(e.target.value);
            }}
          />
        </div>
      ) : null}
      {shown.length === 0 ? (
        <p className="text-xs text-muted-foreground">{T.noMatch}</p>
      ) : (
        <div className="overflow-x-auto rounded-md border bg-surface">
          <table className="w-full text-xs">
            <thead>
              <tr className="border-b border-rule text-left text-muted-foreground">
                <th scope="col" className="px-3 py-1.5 font-medium">
                  {T.cols.name}
                </th>
                <th scope="col" className="px-3 py-1.5 text-right font-medium">
                  {T.cols.rows}
                </th>
                <th scope="col" className="px-3 py-1.5 text-right font-medium">
                  {T.cols.count}
                </th>
              </tr>
            </thead>
            <tbody>
              {shown.map((t) => (
                <tr key={t.name} className="border-t border-rule first:border-t-0">
                  <td className="px-3 py-1 font-mono break-all">{t.name}</td>
                  <td className="px-3 py-1 text-right font-mono tabular-nums">
                    {t.rows == null ? (
                      <span className="text-muted-foreground">—</span>
                    ) : (
                      `${t.estimate || t.approximate ? "≈ " : ""}${numberFormat.format(t.rows)}`
                    )}
                  </td>
                  <td className="px-3 py-1 text-right">
                    <CountBadges table={t} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {!all && matching.length > TABLE_PREVIEW ? (
        <Button
          variant="ghost"
          size="sm"
          className="self-start"
          onClick={() => {
            setAll(true);
          }}
        >
          {T.showAll(matching.length)}
        </Button>
      ) : null}
    </div>
  );
}

/** One item's statistics: header facts, objects, findings and tables. */
export function ItemStatsCard({ item, stats }: { item: Item; stats: ItemStats }) {
  const exact = stats.tables.filter((t) => t.exact).length;
  const estimate = stats.tables.filter((t) => t.estimate).length;
  const unknown = stats.tables.filter((t) => t.unknown).length;
  const name = item.kind === "GLOBALS" ? T.globals : (item.database ?? "");
  const summary = T.summary(exact, estimate, unknown);
  return (
    <details
      data-testid="item-stats"
      className="group rounded-md border bg-card"
      open={stats.warnings.length > 0 || stats.tablesTruncated}
    >
      <summary className="flex cursor-pointer flex-wrap items-center gap-x-4 gap-y-1 px-4 py-2.5">
        <span className="font-mono text-sm font-medium">{name}</span>
        <span className="font-mono text-2xs text-muted-foreground uppercase">
          {T.level[stats.level]}
        </span>
        <span
          className="text-xs text-muted-foreground"
          title={T.consistencyHint[stats.consistency]}
        >
          {T.consistency[stats.consistency]}
        </span>
        {item.kind === "DATABASE" ? (
          <span className="font-mono text-xs">
            {T.tables}: {T.tablesOf(stats.tables.length, stats.tablesTotal)}
          </span>
        ) : null}
        {summary ? <span className="text-xs text-muted-foreground">{summary}</span> : null}
        {stats.warnings.length > 0 ? (
          <span className="ml-auto rounded-sm border border-status-partial/45 bg-status-partial-soft px-1.5 text-2xs font-medium text-status-partial-fg">
            {stats.warnings.length} warning{stats.warnings.length === 1 ? "" : "s"}
          </span>
        ) : null}
      </summary>
      <div className="flex flex-col gap-4 border-t border-rule px-4 py-3">
        {stats.tablesTruncated ? (
          <p
            role="note"
            className="rounded-md border border-l-4 border-status-partial/40 border-l-status-partial bg-status-partial-soft/50 px-3 py-2 text-xs text-status-partial-fg"
          >
            {T.truncated(stats.tables.length, stats.tablesTotal)}
          </p>
        ) : null}
        {stats.objects.length > 0 ? (
          <div className="flex flex-col gap-1.5">
            <h5 className="eyebrow">{T.objects}</h5>
            <dl className="grid grid-cols-[repeat(auto-fill,minmax(9rem,1fr))] gap-x-4 gap-y-1">
              {stats.objects.map((o) => (
                <div
                  key={o.kind}
                  className="flex items-baseline justify-between gap-2 border-b border-dotted border-rule py-0.5"
                >
                  <dt className="text-xs text-muted-foreground">{o.kind.replaceAll("_", " ")}</dt>
                  <dd className="font-mono text-xs tabular-nums">{numberFormat.format(o.count)}</dd>
                </div>
              ))}
            </dl>
          </div>
        ) : null}
        <FindingList findings={stats.warnings} kind="warning" />
        <FindingList findings={stats.notices} kind="notice" />
        <TableList stats={stats} />
        <p className="font-mono text-2xs text-muted-foreground">
          {T.recorded(formatDateTime(stats.recordedAt))}
        </p>
      </div>
    </details>
  );
}

/** Per-item statistics of a finished run (RunItem.stats, D-187). */
export function RunItemStatsSection({ runId, active }: { runId: string; active: boolean }) {
  const { data, error, loading } = useQuery(
    RunItemStatsQuery,
    active ? skipToken : { variables: { id: runId } },
  );
  const items = (data?.run?.items ?? []).filter(
    (i): i is Item & { stats: ItemStats } => i.stats != null,
  );
  return (
    <div className="flex flex-col gap-2">
      <h3 className="eyebrow flex items-center gap-1.5">
        <TableProperties aria-hidden="true" className="size-3.5" /> {T.heading}
      </h3>
      <p className="text-xs text-muted-foreground">{T.lead}</p>
      {active ? (
        <p className="text-sm text-muted-foreground">{T.pending}</p>
      ) : error && !data ? (
        <p className="text-sm text-status-failed-fg">{userMessage(error)}</p>
      ) : loading && !data ? (
        <Skeleton className="h-10 w-full" />
      ) : items.length === 0 ? (
        <p className="text-sm text-muted-foreground">{T.none}</p>
      ) : (
        <div className="flex flex-col gap-2">
          {items.map((i) => (
            <ItemStatsCard key={i.id} item={i} stats={i.stats} />
          ))}
        </div>
      )}
    </div>
  );
}
