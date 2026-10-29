import { useQuery } from "@apollo/client/react";
import { Filter, Link2, ScrollText, ShieldCheck, ShieldQuestion, ShieldX, X } from "lucide-react";
import { useId, useState, type ComponentProps, type SyntheticEvent } from "react";

import { CopyValue } from "@/components/copy-value";
import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { Tag } from "@/components/status/tag";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { NativeSelect } from "@/components/ui/native-select";
import { Skeleton } from "@/components/ui/skeleton";
import { Pager } from "@/features/jobs/pager";
import { useCursorPager } from "@/features/jobs/use-cursor-pager";
import type { AdminAuditLogQuery, AuditActorKind } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { formatDateTime, formatRelative } from "@/lib/format";
import { messages } from "@/lib/messages";
import { cn } from "@/lib/utils";

import { AuditChainQuery, AuditLogQuery } from "../operations";
import { docs } from "../docs";
import { Command, DocLink } from "../ui";
import {
  ACTION_PREFIXES,
  activeFilters,
  emptyDraft,
  prettyDetails,
  AUDIT_PAGE_SIZE,
  shortHash,
  toAuditFilter,
  validateDraft,
  type AuditFilterDraft,
  type DraftErrors,
} from "./model";

type Entry = AdminAuditLogQuery["auditLog"]["edges"][number]["node"];

const T = {
  eyebrow: "Administration · Security",
  title: "Audit log",
  lead: "Every security-relevant action, newest first. Entries are hash chained and append-only; details are redacted when recorded and never hold secrets.",
  guide: "Audit guide",
  filters: "Filters",
  action: "Action",
  actionHint: "Exact (user.created) or a prefix ending in “.” or “*”.",
  actorKind: "Actor",
  anyActor: "Anyone",
  actorKinds: {
    USER: "User",
    SYSTEM: "bekup (system)",
    CLI: "Host operator (CLI)",
  } satisfies Record<AuditActorKind, string>,
  actorUserId: "Actor user ID",
  targetType: "Target type",
  targetId: "Target ID",
  from: "From",
  to: "Before",
  apply: "Apply filters",
  clear: "Clear",
  quick: "Quick filters",
  colTime: "Time",
  colAction: "Action",
  colActor: "Actor",
  colTarget: "Target",
  colIp: "Address",
  colHash: "Chain",
  empty: "No entries match",
  emptyLead: "Widen the time range or clear a filter.",
  pager: "Audit log pages",
  open: (action: string, when: string) => `Details of ${action} at ${when}`,
  detailTitle: "Audit entry",
  details: "Details (redacted)",
  hash: "Chain hash",
  copyHash: "Copy chain hash",
  id: "Entry",
  chainTitle: "Chain integrity",
  chainOk: "Last verification passed",
  chainFailed: "Chain verification failed",
  chainNever: "Not verified yet",
  chainNeverLead:
    "The periodic check runs every hour (new entries) and in full daily and after every restart; its first result appears here.",
  chainOkLead: (when: string, full: boolean) =>
    `${full ? "Full" : "Incremental"} check ${when}. This reflects what the server recorded; it is not a proof.`,
  chainFailedLead: (when: string, full: boolean) =>
    `${full ? "Full" : "Incremental"} check ${when} found a problem. Treat the log as tampered with until you have checked it on the host.`,
  finding: "Finding",
  verifiedThrough: "Verified through",
  head: "Newest entry",
  signedHead: "Latest signed head",
  noSignedHead: "None yet (tail truncation cannot be detected without one)",
  showFailures: "Show audit.verify.failed entries",
  chainUnknown: "Chain status unavailable",
  chainProof: "For a full offline proof against the signed head, run on the host:",
} as const;

/** The audit log viewer (admin, D-101) with the chain status (D-190, D-196). */
export function AuditLogPage() {
  const [draft, setDraft] = useState<AuditFilterDraft>(emptyDraft);
  const [applied, setApplied] = useState<AuditFilterDraft>(emptyDraft);
  const [errors, setErrors] = useState<DraftErrors>({});
  const [selected, setSelected] = useState<Entry | null>(null);
  const filter = toAuditFilter(applied);
  const pager = useCursorPager(JSON.stringify(filter));
  const { data, error, loading, refetch } = useQuery(AuditLogQuery, {
    variables: { filter, first: AUDIT_PAGE_SIZE, after: pager.after },
    fetchPolicy: "cache-and-network",
  });

  const apply = (next: AuditFilterDraft) => {
    const errs = validateDraft(next);
    setErrors(errs);
    if (Object.keys(errs).length > 0) return;
    setDraft(next);
    setApplied(next);
  };

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        eyebrow={T.eyebrow}
        title={T.title}
        description={T.lead}
        actions={<DocLink href={docs.audit}>{T.guide}</DocLink>}
      />
      <ChainStatus
        onShowFailures={() => {
          apply({ ...emptyDraft, action: "audit.verify.failed" });
        }}
      />
      <FilterBar
        draft={draft}
        errors={errors}
        active={activeFilters(applied)}
        onChange={setDraft}
        onApply={() => {
          apply(draft);
        }}
        onQuick={(prefix) => {
          apply({ ...draft, action: prefix });
        }}
        onClear={() => {
          setErrors({});
          setDraft(emptyDraft);
          setApplied(emptyDraft);
        }}
      />
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
        <div className="flex flex-col gap-2" aria-busy="true">
          {Array.from({ length: 6 }, (_, i) => (
            <Skeleton key={i} className="h-10 w-full" />
          ))}
        </div>
      ) : data.auditLog.edges.length === 0 ? (
        <EmptyState icon={ScrollText} title={T.empty}>
          {T.emptyLead}
        </EmptyState>
      ) : (
        <div className={cn("flex flex-col gap-3", loading && "opacity-70")} aria-busy={loading}>
          <Ledger entries={data.auditLog.edges.map((e) => e.node)} onOpen={setSelected} />
          <Pager
            page={pager.page}
            hasNext={data.auditLog.pageInfo.hasNextPage}
            onPrev={pager.prev}
            onNext={() => {
              pager.next(data.auditLog.pageInfo.endCursor);
            }}
            label={T.pager}
          />
        </div>
      )}
      <EntryDrawer
        entry={selected}
        onClose={() => {
          setSelected(null);
        }}
      />
    </div>
  );
}

function ChainStatus({ onShowFailures }: { onShowFailures: () => void }) {
  const { data, error } = useQuery(AuditChainQuery, { fetchPolicy: "cache-and-network" });
  const st = data?.auditChainStatus;
  const v = st?.lastVerification;
  const state =
    error && !data ? "unknown" : !st ? "loading" : !v ? "never" : v.ok ? "ok" : "failed";
  const head = st?.headId ? st : null;
  const signed = st?.latestSignedHead;
  return (
    <section
      aria-label={T.chainTitle}
      data-testid="audit-chain-status"
      data-state={state}
      className={cn(
        "grid gap-4 rounded-lg border p-4 md:grid-cols-[minmax(0,1fr)_minmax(0,22rem)]",
        state === "failed" ? "border-status-failed/50 bg-status-failed-soft" : "bg-card",
      )}
    >
      <div className="flex items-start gap-3">
        <span
          aria-hidden="true"
          className={cn(
            "inline-flex size-9 shrink-0 items-center justify-center rounded-md border",
            state === "failed"
              ? "border-status-failed/50 text-status-failed"
              : state === "ok"
                ? "border-status-success/40 text-status-success"
                : "border-border text-muted-foreground",
          )}
        >
          {state === "failed" ? (
            <ShieldX className="size-4" />
          ) : state === "ok" ? (
            <ShieldCheck className="size-4" />
          ) : (
            <ShieldQuestion className="size-4" />
          )}
        </span>
        <div className="flex min-w-0 flex-col gap-1">
          <p className="eyebrow">{T.chainTitle}</p>
          {state === "loading" ? (
            <Skeleton className="h-5 w-64" />
          ) : state === "unknown" ? (
            <p className="font-medium">{T.chainUnknown}</p>
          ) : state === "never" || !v ? (
            <>
              <p className="font-medium">{T.chainNever}</p>
              <p className="text-sm text-muted-foreground">{T.chainNeverLead}</p>
            </>
          ) : v.ok ? (
            <>
              <p className="font-medium">{T.chainOk}</p>
              <p className="text-sm text-muted-foreground" title={formatDateTime(v.checkedAt)}>
                {T.chainOkLead(formatRelative(v.checkedAt), v.full)}
              </p>
            </>
          ) : (
            <>
              <p className="font-medium text-status-failed-fg">{T.chainFailed}</p>
              <p className="text-sm" title={formatDateTime(v.checkedAt)}>
                {T.chainFailedLead(formatRelative(v.checkedAt), v.full)}
              </p>
              <Button
                variant="outline"
                size="sm"
                className="mt-1 self-start"
                onClick={onShowFailures}
              >
                {T.showFailures}
              </Button>
            </>
          )}
          {st ? (
            <dl className="mt-2 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-xs">
              {v?.finding ? (
                <>
                  <dt className="text-muted-foreground">{T.finding}</dt>
                  <dd className="font-mono" data-testid="audit-chain-finding">
                    {v.finding}
                  </dd>
                </>
              ) : null}
              {v?.verifiedThroughId ? (
                <>
                  <dt className="text-muted-foreground">{T.verifiedThrough}</dt>
                  <dd className="font-mono">#{v.verifiedThroughId}</dd>
                </>
              ) : null}
              {head?.headId ? (
                <>
                  <dt className="text-muted-foreground">{T.head}</dt>
                  <dd className="flex min-w-0 flex-wrap items-center gap-x-2 font-mono">
                    <span>#{head.headId}</span>
                    {head.headHash ? (
                      <span title={head.headHash}>{shortHash(head.headHash)}</span>
                    ) : null}
                    {head.headAt ? (
                      <time
                        dateTime={head.headAt}
                        title={formatDateTime(head.headAt)}
                        className="font-sans text-muted-foreground"
                      >
                        {formatRelative(head.headAt)}
                      </time>
                    ) : null}
                  </dd>
                </>
              ) : null}
              <dt className="text-muted-foreground">{T.signedHead}</dt>
              <dd data-testid="audit-signed-head">
                {signed ? (
                  <span className="flex flex-wrap items-center gap-x-2">
                    <span className="font-mono">#{signed.chainId}</span>
                    <time dateTime={signed.signedAt} title={formatDateTime(signed.signedAt)}>
                      {formatRelative(signed.signedAt)}
                    </time>
                    <span className="font-mono text-muted-foreground">key {signed.keyId}</span>
                  </span>
                ) : (
                  <span className="text-muted-foreground">{T.noSignedHead}</span>
                )}
              </dd>
            </dl>
          ) : null}
        </div>
      </div>
      <div className="flex flex-col gap-2 text-sm text-muted-foreground">
        <p>{T.chainProof}</p>
        <Command>bekup audit verify</Command>
      </div>
    </section>
  );
}

function FilterBar({
  draft,
  errors,
  active,
  onChange,
  onApply,
  onQuick,
  onClear,
}: {
  draft: AuditFilterDraft;
  errors: DraftErrors;
  active: number;
  onChange: (d: AuditFilterDraft) => void;
  onApply: () => void;
  onQuick: (prefix: string) => void;
  onClear: () => void;
}) {
  const base = useId();
  const field = (
    key: keyof AuditFilterDraft,
    label: string,
    props: ComponentProps<typeof Input> = {},
  ) => {
    const id = `${base}-${key}`;
    const err = errors[key];
    return (
      <div className="grid gap-1.5">
        <Label htmlFor={id} className="text-xs">
          {label}
        </Label>
        <Input
          id={id}
          value={draft[key]}
          aria-invalid={err ? true : undefined}
          aria-describedby={err ? `${id}-err` : undefined}
          onChange={(e) => {
            onChange({ ...draft, [key]: e.target.value });
          }}
          autoComplete="off"
          spellCheck={false}
          {...props}
        />
        {err ? (
          <p id={`${id}-err`} className="text-xs text-destructive">
            {err}
          </p>
        ) : null}
      </div>
    );
  };
  const submit = (e: SyntheticEvent) => {
    e.preventDefault();
    onApply();
  };
  return (
    <form
      noValidate
      onSubmit={submit}
      aria-label={T.filters}
      className="flex flex-col gap-4 rounded-lg border bg-surface p-4"
    >
      <div className="flex items-center gap-2">
        <Filter aria-hidden="true" className="size-3.5 text-muted-foreground" />
        <p className="eyebrow">{T.filters}</p>
        {active > 0 ? <Tag tone="info">{`${String(active)} active`}</Tag> : null}
      </div>
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <div className="sm:col-span-2">
          {field("action", T.action, {
            placeholder: "auth.  ·  user.created",
            className: "font-mono",
          })}
          <p className="mt-1 text-2xs text-muted-foreground">{T.actionHint}</p>
        </div>
        <div className="grid gap-1.5">
          <Label htmlFor={`${base}-actorKind`} className="text-xs">
            {T.actorKind}
          </Label>
          <NativeSelect
            id={`${base}-actorKind`}
            value={draft.actorKind}
            onChange={(e) => {
              onChange({ ...draft, actorKind: e.target.value as AuditActorKind | "" });
            }}
          >
            <option value="">{T.anyActor}</option>
            {(Object.keys(T.actorKinds) as AuditActorKind[]).map((k) => (
              <option key={k} value={k}>
                {T.actorKinds[k]}
              </option>
            ))}
          </NativeSelect>
        </div>
        {field("actorUserId", T.actorUserId, { className: "font-mono", placeholder: "UUID" })}
        {field("targetType", T.targetType, { className: "font-mono", placeholder: "job" })}
        {field("targetId", T.targetId, { className: "font-mono" })}
        {field("from", T.from, { type: "datetime-local" })}
        {field("to", T.to, { type: "datetime-local" })}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <span className="eyebrow mr-1">{T.quick}</span>
        {ACTION_PREFIXES.map((p) => (
          <button
            key={p}
            type="button"
            aria-pressed={draft.action === p}
            onClick={() => {
              onQuick(p);
            }}
            className={cn(
              "h-6 rounded-sm border px-2 font-mono text-2xs transition-colors hover:border-border-strong focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none",
              draft.action === p
                ? "border-primary/50 bg-accent text-foreground"
                : "text-muted-foreground",
            )}
          >
            {p}
          </button>
        ))}
        <span className="ml-auto flex gap-2">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={onClear}
            disabled={active === 0 && activeFilters(draft) === 0}
          >
            <X aria-hidden="true" /> {T.clear}
          </Button>
          <Button type="submit" size="sm">
            {T.apply}
          </Button>
        </span>
      </div>
    </form>
  );
}

function actorLabel(e: Entry): string {
  if (e.actorKind === "USER") return e.actorUserId ?? "user";
  return T.actorKinds[e.actorKind];
}

/** The entries as a ledger: a chain rail with one link per entry. */
function Ledger({ entries, onOpen }: { entries: readonly Entry[]; onOpen: (e: Entry) => void }) {
  return (
    <div className="overflow-x-auto rounded-lg border bg-card">
      <table className="w-full min-w-[52rem] border-collapse text-sm">
        <caption className="sr-only">{T.title}</caption>
        <thead>
          <tr className="border-b border-rule text-left">
            <th scope="col" className="eyebrow w-28 py-2.5 pr-2 pl-4 font-normal">
              {T.colHash}
            </th>
            <th scope="col" className="eyebrow py-2.5 pr-3 font-normal">
              {T.colTime}
            </th>
            <th scope="col" className="eyebrow py-2.5 pr-3 font-normal">
              {T.colAction}
            </th>
            <th scope="col" className="eyebrow py-2.5 pr-3 font-normal">
              {T.colActor}
            </th>
            <th scope="col" className="eyebrow py-2.5 pr-3 font-normal">
              {T.colTarget}
            </th>
            <th scope="col" className="eyebrow py-2.5 pr-4 font-normal">
              {T.colIp}
            </th>
          </tr>
        </thead>
        <tbody>
          {entries.map((e, i) => (
            <tr
              key={e.id}
              data-testid="audit-row"
              className="group border-b border-rule/60 last:border-b-0 hover:bg-accent/50"
            >
              <td className="relative py-0 pr-2 pl-4">
                {/* The chain rail: a vertical line through every link. */}
                <span
                  aria-hidden="true"
                  className={cn(
                    "absolute left-[1.4rem] w-px bg-border-strong/60",
                    i === 0
                      ? "top-1/2 bottom-0"
                      : i === entries.length - 1
                        ? "top-0 bottom-1/2"
                        : "inset-y-0",
                  )}
                />
                <span className="relative flex items-center gap-2">
                  <span
                    aria-hidden="true"
                    className="z-10 inline-flex size-3 items-center justify-center rounded-full border border-border-strong bg-card group-hover:border-primary"
                  >
                    <span className="size-1 rounded-full bg-muted-foreground group-hover:bg-primary" />
                  </span>
                  <span className="font-mono text-2xs text-muted-foreground" title={e.hash}>
                    {shortHash(e.hash)}
                  </span>
                </span>
              </td>
              <td className="py-2 pr-3 whitespace-nowrap">
                <span className="font-mono text-xs tabular-nums" title={formatRelative(e.ts)}>
                  {formatDateTime(e.ts)}
                </span>
              </td>
              <td className="py-2 pr-3">
                <button
                  type="button"
                  aria-label={T.open(e.action, formatDateTime(e.ts))}
                  onClick={() => {
                    onOpen(e);
                  }}
                  className={cn(
                    "rounded-xs font-mono text-xs font-medium underline-offset-4 hover:underline focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none",
                    e.action.startsWith("security.") || e.action.endsWith(".failed")
                      ? "text-status-failed-fg"
                      : "text-foreground",
                  )}
                >
                  {e.action}
                </button>
              </td>
              <td className="max-w-48 truncate py-2 pr-3 text-xs" title={actorLabel(e)}>
                <span className="eyebrow mr-1.5">{e.actorKind}</span>
                {e.actorKind === "USER" && e.actorUserId ? (
                  <span className="font-mono text-2xs text-muted-foreground">
                    {e.actorUserId.slice(0, 8)}
                  </span>
                ) : null}
              </td>
              <td className="max-w-56 truncate py-2 pr-3 text-xs">
                {e.targetType ? (
                  <>
                    <span className="text-muted-foreground">{e.targetType}</span>
                    {e.targetId ? (
                      <span className="ml-1.5 font-mono text-2xs">{e.targetId}</span>
                    ) : null}
                  </>
                ) : (
                  <span className="text-muted-foreground">—</span>
                )}
              </td>
              <td className="py-2 pr-4 font-mono text-2xs text-muted-foreground">{e.ip ?? "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function EntryDrawer({ entry, onClose }: { entry: Entry | null; onClose: () => void }) {
  return (
    <Dialog
      open={entry !== null}
      onOpenChange={(o) => {
        if (!o) onClose();
      }}
    >
      <DialogContent className="top-0 right-0 left-auto flex h-dvh max-h-dvh w-full translate-x-0 translate-y-0 flex-col gap-5 overflow-y-auto rounded-none border-y-0 border-r-0 sm:max-w-lg">
        {entry ? (
          <>
            <DialogHeader>
              <p className="eyebrow flex items-center gap-1.5">
                <Link2 aria-hidden="true" className="size-3.5" /> {T.detailTitle}
              </p>
              <DialogTitle className="font-mono text-lg font-medium break-all">
                {entry.action}
              </DialogTitle>
              <DialogDescription>{formatDateTime(entry.ts)}</DialogDescription>
            </DialogHeader>
            <dl className="grid grid-cols-[7rem_minmax(0,1fr)] gap-x-4 gap-y-2.5 text-sm">
              <dt className="eyebrow self-center">{T.id}</dt>
              <dd className="font-mono text-xs break-all">{entry.id}</dd>
              <dt className="eyebrow self-center">{T.colActor}</dt>
              <dd className="text-xs">
                {T.actorKinds[entry.actorKind]}
                {entry.actorUserId ? (
                  <span className="ml-2 font-mono break-all">{entry.actorUserId}</span>
                ) : null}
              </dd>
              <dt className="eyebrow self-center">{T.colTarget}</dt>
              <dd className="text-xs break-all">
                {entry.targetType ?? "—"}
                {entry.targetId ? <span className="ml-2 font-mono">{entry.targetId}</span> : null}
              </dd>
              <dt className="eyebrow self-center">{T.colIp}</dt>
              <dd className="font-mono text-xs">{entry.ip ?? "—"}</dd>
            </dl>
            <div className="flex flex-col gap-2">
              <p className="eyebrow">{T.details}</p>
              <pre
                data-testid="audit-details"
                className="max-h-[40vh] overflow-auto rounded-md border bg-surface-sunken p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap"
              >
                {prettyDetails(entry.details)}
              </pre>
            </div>
            {entry.hash ? (
              <div className="flex flex-col gap-2">
                <p className="eyebrow">{T.hash}</p>
                <CopyValue value={entry.hash} label={T.copyHash} />
              </div>
            ) : null}
          </>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}
