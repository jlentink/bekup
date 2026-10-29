import { useQuery } from "@apollo/client/react";
import {
  AlertOctagon,
  ArrowRight,
  Info,
  ShieldAlert,
  ShieldCheck,
  TriangleAlert,
  XCircle,
  type LucideIcon,
} from "lucide-react";
import { useState, type ReactNode } from "react";
import { Link } from "react-router";

import { EmptyState } from "@/components/empty-state";
import { Tag, type Tone } from "@/components/status/tag";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Pager } from "@/features/jobs/pager";
import { useCursorPager } from "@/features/jobs/use-cursor-pager";
import type { AdminSecurityInboxQuery, NotificationSeverity } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { formatDateTime, formatRelative } from "@/lib/format";
import { messages } from "@/lib/messages";
import { cn } from "@/lib/utils";

import { SecurityInboxQuery as InboxQuery, SecurityAlertTypesQuery } from "../operations";
import { docs } from "../docs";
import { DocLink } from "../ui";
import { ALERT_PAGE, alertPath } from "./alert-types";

type Item = AdminSecurityInboxQuery["notifications"]["edges"][number]["node"];

const T = {
  title: "Security alerts",
  lead: "Security events from your inbox: weakened settings, impersonation signs, tampering, break-glass use and credential attacks. They always reach every admin by inbox and e-mail and cannot be turned off.",
  guide: "Alert reference",
  filter: "Severity",
  all: "All",
  scan: (n: number) => `Up to ${String(n)} security alerts per page, newest first.`,
  scanPage: (page: number) => `Page ${String(page)}.`,
  severityNote: "Severity counts cover this page.",
  unreadOnly: "Unread only",
  emptyTitle: "No security alerts here",
  emptyLead: "No security alert in your inbox matches this view.",
  unread: "Unread",
  open: "Open",
  pager: "Inbox pages",
  typesTitle: "Alert types",
  typesLead: "What raises a security.system alert, its severity and how often it repeats.",
  colAlert: "Alert",
  colSeverity: "Severity",
  colSent: "Sent",
  typesFailed: "The alert reference could not be loaded.",
  inbox: "Open the inbox",
} as const;

const SEVERITY: Record<NotificationSeverity, { label: string; tone: Tone; icon: LucideIcon }> = {
  CRITICAL: { label: "Critical", tone: "danger", icon: AlertOctagon },
  ERROR: { label: "Error", tone: "danger", icon: XCircle },
  WARNING: { label: "Warning", tone: "warning", icon: TriangleAlert },
  INFO: { label: "Info", tone: "info", icon: Info },
};
const SEVERITIES: readonly NotificationSeverity[] = ["CRITICAL", "ERROR", "WARNING", "INFO"];

/**
 * Security alerts of the admin's own inbox (D-190): the server filters to
 * group SECURITY (D-196); severity is a client-side view of the page.
 */
export function SecurityAlertsPage() {
  const [severity, setSeverity] = useState<NotificationSeverity | null>(null);
  const [unreadOnly, setUnreadOnly] = useState(false);
  const pager = useCursorPager(`security-inbox:${String(unreadOnly)}`);
  const { data, error, refetch } = useQuery(InboxQuery, {
    variables: {
      filter: { group: "SECURITY", unreadOnly },
      first: ALERT_PAGE,
      after: pager.after,
    },
    fetchPolicy: "cache-and-network",
  });
  const all = data?.notifications.edges.map((e) => e.node) ?? [];
  const items = severity ? all.filter((n) => n.severity === severity) : all;
  const counts = Object.fromEntries(
    SEVERITIES.map((s) => [s, all.filter((n) => n.severity === s).length]),
  ) as Record<NotificationSeverity, number>;

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-start justify-between gap-3 border-b border-rule pb-4">
        <div className="flex max-w-prose flex-col gap-1">
          <h2 className="font-display text-3xl">{T.title}</h2>
          <p className="text-sm text-muted-foreground">{T.lead}</p>
        </div>
        <DocLink href={docs.securityAlerts}>{T.guide}</DocLink>
      </div>

      <div role="group" aria-label={T.filter} className="flex flex-wrap items-center gap-2">
        <span className="eyebrow mr-1">{T.filter}</span>
        <SeverityChip
          active={severity === null}
          onClick={() => {
            setSeverity(null);
          }}
        >
          {T.all} <span className="font-mono text-2xs">{all.length}</span>
        </SeverityChip>
        {SEVERITIES.map((s) => (
          <SeverityChip
            key={s}
            active={severity === s}
            onClick={() => {
              setSeverity(s);
            }}
          >
            {SEVERITY[s].label} <span className="font-mono text-2xs">{counts[s]}</span>
          </SeverityChip>
        ))}
        <span aria-hidden="true" className="mx-1 h-4 w-px bg-rule" />
        <SeverityChip
          active={unreadOnly}
          onClick={() => {
            setUnreadOnly((u) => !u);
          }}
        >
          {T.unreadOnly}
        </SeverityChip>
      </div>

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
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-16 w-full" />
        </div>
      ) : items.length === 0 ? (
        <EmptyState icon={ShieldCheck} title={T.emptyTitle}>
          {T.emptyLead}
        </EmptyState>
      ) : (
        <ol className="flex flex-col divide-y divide-rule rounded-lg border bg-card">
          {items.map((n) => (
            <AlertRow key={n.id} item={n} />
          ))}
        </ol>
      )}
      {data ? (
        <div className="flex flex-wrap items-center justify-between gap-3 text-xs text-muted-foreground">
          <span>
            {T.scan(ALERT_PAGE)} {T.severityNote} {pager.page > 1 ? T.scanPage(pager.page) : ""}
          </span>
          <Pager
            page={pager.page}
            hasNext={data.notifications.pageInfo.hasNextPage}
            onPrev={pager.prev}
            onNext={() => {
              pager.next(data.notifications.pageInfo.endCursor);
            }}
            label={T.pager}
          />
        </div>
      ) : null}

      <section aria-labelledby="alert-types-heading" className="flex flex-col gap-3 pt-4">
        <div className="flex flex-col gap-1">
          <h3 id="alert-types-heading" className="flex items-center gap-2 font-semibold">
            <ShieldAlert aria-hidden="true" className="size-4 text-muted-foreground" />
            {T.typesTitle}
          </h3>
          <p className="text-sm text-muted-foreground">{T.typesLead}</p>
        </div>
        <AlertTypesTable />
      </section>
    </div>
  );
}

/** The server's reference list of alert kinds (securityAlertTypes). */
function AlertTypesTable() {
  const { data, loading, error } = useQuery(SecurityAlertTypesQuery);
  if (loading && !data) return <Skeleton className="h-48" />;
  if (!data) {
    return (
      <Alert variant="destructive">
        <AlertDescription>{error ? userMessage(error) : T.typesFailed}</AlertDescription>
      </Alert>
    );
  }
  return (
    <div className="overflow-x-auto rounded-lg border">
      <table className="w-full min-w-[40rem] text-sm">
        <thead>
          <tr className="border-b border-rule text-left">
            <th scope="col" className="eyebrow px-4 py-2 font-normal">
              {T.colAlert}
            </th>
            <th scope="col" className="eyebrow px-3 py-2 font-normal">
              {T.colSeverity}
            </th>
            <th scope="col" className="eyebrow px-4 py-2 font-normal">
              {T.colSent}
            </th>
          </tr>
        </thead>
        <tbody>
          {data.securityAlertTypes.map((a) => (
            <tr key={a.kind} className="border-b border-rule/60 last:border-b-0">
              <td className="px-4 py-2">
                <span className="flex flex-col gap-0.5">
                  {a.description}
                  <code className="font-mono text-2xs text-muted-foreground">{a.kind}</code>
                </span>
              </td>
              <td className="px-3 py-2">
                <Tag tone={SEVERITY[a.severity].tone}>{SEVERITY[a.severity].label}</Tag>
              </td>
              <td className="px-4 py-2 text-xs text-muted-foreground">{a.repeat}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function SeverityChip({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        "inline-flex h-7 items-center gap-1.5 rounded-sm border px-2.5 text-xs transition-colors hover:border-border-strong focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none",
        active ? "border-primary/50 bg-accent text-foreground" : "text-muted-foreground",
      )}
    >
      {children}
    </button>
  );
}

function AlertRow({ item }: { item: Item }) {
  const s = SEVERITY[item.severity];
  const Icon = s.icon;
  const path = alertPath(item.link);
  return (
    <li
      data-testid="security-alert"
      className="grid grid-cols-[auto_minmax(0,1fr)_auto] gap-3 px-4 py-3"
    >
      <span
        aria-hidden="true"
        className={cn(
          "mt-0.5 inline-flex size-7 items-center justify-center rounded-sm border",
          s.tone === "danger"
            ? "border-status-failed/45 text-status-failed"
            : s.tone === "warning"
              ? "border-status-partial/45 text-status-partial"
              : "border-status-running/40 text-status-running",
        )}
      >
        <Icon className="size-3.5" />
      </span>
      <div className="flex min-w-0 flex-col gap-1">
        <div className="flex flex-wrap items-center gap-2">
          <p className={cn("font-medium", !item.readAt && "font-semibold")}>{item.title}</p>
          <Tag tone={s.tone}>{s.label}</Tag>
          {!item.readAt ? <span className="eyebrow text-primary">{T.unread}</span> : null}
        </div>
        <p className="text-sm text-muted-foreground">{item.body}</p>
        <p className="font-mono text-2xs text-muted-foreground">
          {item.type} ·{" "}
          <time dateTime={item.createdAt} title={formatDateTime(item.createdAt)}>
            {formatRelative(item.createdAt)}
          </time>
        </p>
      </div>
      {path ? (
        <Button asChild variant="ghost" size="sm" className="self-center">
          <Link to={path}>
            {T.open} <ArrowRight aria-hidden="true" />
          </Link>
        </Button>
      ) : (
        <span />
      )}
    </li>
  );
}
