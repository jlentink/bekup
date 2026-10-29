import { useApolloClient, useMutation, useQuery } from "@apollo/client/react";
import { ArrowUpRight, Check, CheckCheck, Inbox, Settings2, Trash2 } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router";
import { toast } from "sonner";

import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Pager } from "@/features/jobs/pager";
import { useCursorPager } from "@/features/jobs/use-cursor-pager";
import { getFragmentData } from "@/graphql/gen";
import type { NotificationFieldsFragment } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { cn } from "@/lib/utils";

import { applyMarked, refetchNotificationLists } from "./cache";
import { notificationsMessages as M } from "./messages";
import { subjectKind, subjectPath } from "./model";
import { NotificationText, SeverityMark } from "./notification-item";
import {
  DeleteNotificationMutation,
  MarkNotificationsReadMutation,
  NotificationFields,
  NotificationsInboxQuery,
} from "./operations";

const PAGE_SIZE = 25;

type Filter = "all" | "unread";

function InboxRow({
  n,
  onRead,
  onDelete,
}: {
  n: NotificationFieldsFragment;
  onRead: (n: NotificationFieldsFragment) => void;
  onDelete: (n: NotificationFieldsFragment) => void;
}) {
  const path = subjectPath(n.link);
  const unread = n.readAt === null;
  return (
    <li
      data-testid="inbox-row"
      data-unread={unread}
      className={cn(
        "group grid grid-cols-[auto_minmax(0,1fr)_auto] items-start gap-x-4 gap-y-2 border-b border-rule px-4 py-4 last:border-b-0 sm:px-5",
        unread && "bg-accent/30",
      )}
    >
      <SeverityMark severity={n.severity} />
      <div className="flex min-w-0 flex-col gap-2">
        <NotificationText n={n} />
        {path ? (
          <Link
            to={path}
            onClick={() => {
              if (unread) onRead(n);
            }}
            className="inline-flex w-fit items-center gap-1 text-xs font-medium text-primary underline-offset-4 hover:underline"
          >
            {M.subject[subjectKind(path)]}
            <ArrowUpRight aria-hidden="true" className="size-3" />
          </Link>
        ) : null}
      </div>
      <div className="flex items-center gap-0.5 opacity-100 transition-opacity sm:opacity-60 sm:group-hover:opacity-100 sm:focus-within:opacity-100">
        {unread ? (
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={M.inbox.markRead(n.title)}
            title={M.inbox.markRead(n.title)}
            onClick={() => {
              onRead(n);
            }}
          >
            <Check aria-hidden="true" />
          </Button>
        ) : null}
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={M.inbox.delete(n.title)}
          title={M.inbox.delete(n.title)}
          onClick={() => {
            onDelete(n);
          }}
        >
          <Trash2 aria-hidden="true" />
        </Button>
      </div>
    </li>
  );
}

/** The inbox page: cursor pages, unread filter, mark read and delete (task 6.10). */
export function InboxPage() {
  const client = useApolloClient();
  const [filter, setFilter] = useState<Filter>("all");
  const pager = useCursorPager(filter);
  const { data, loading, error, refetch } = useQuery(NotificationsInboxQuery, {
    variables: { unreadOnly: filter === "unread", first: PAGE_SIZE, after: pager.after },
    fetchPolicy: "cache-and-network",
  });
  const [markRead] = useMutation(MarkNotificationsReadMutation);
  const [remove] = useMutation(DeleteNotificationMutation);

  const unread = data?.unreadNotificationCount ?? 0;
  const items = (data?.notifications.edges ?? []).map((e) =>
    getFragmentData(NotificationFields, e.node),
  );

  const onRead = (n: NotificationFieldsFragment) => {
    markRead({ variables: { ids: [n.id] } })
      .then((r) => {
        const res = r.data?.markNotificationsRead;
        if (res) applyMarked(client.cache, [n.id], res.unreadCount);
      })
      .catch((err: unknown) => toast.error(userMessage(err)));
  };

  const onMarkAll = () => {
    markRead({ variables: { all: true } })
      .then(async () => {
        await refetchNotificationLists(client);
        toast.success(M.inbox.allRead);
      })
      .catch((err: unknown) => toast.error(userMessage(err)));
  };

  const onDelete = (n: NotificationFieldsFragment) => {
    remove({ variables: { id: n.id } })
      .then(async () => {
        await refetchNotificationLists(client);
        toast.success(M.inbox.deleted);
      })
      .catch((err: unknown) => toast.error(userMessage(err)));
  };

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        eyebrow={M.inbox.eyebrow}
        title={M.inbox.title}
        description={M.inbox.lead}
        actions={
          <>
            <Button variant="outline" size="sm" asChild>
              <Link to="/profile/notifications">
                <Settings2 aria-hidden="true" /> {M.inbox.settings}
              </Link>
            </Button>
            <Button size="sm" disabled={unread === 0} onClick={onMarkAll}>
              <CheckCheck aria-hidden="true" /> {M.inbox.markAll}
            </Button>
          </>
        }
      />

      <div className="flex flex-wrap items-center justify-between gap-3">
        <div
          role="radiogroup"
          aria-label={M.inbox.filterLabel}
          className="inline-flex rounded-md border bg-surface-sunken p-0.5"
        >
          {(["all", "unread"] as const).map((f) => (
            <button
              key={f}
              type="button"
              role="radio"
              aria-checked={filter === f}
              onClick={() => {
                setFilter(f);
              }}
              className={cn(
                "h-7 rounded-sm px-3 text-xs font-medium text-muted-foreground transition-colors outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50",
                filter === f && "bg-surface text-foreground shadow-xs",
              )}
            >
              {f === "all" ? M.inbox.all : M.inbox.unreadOnly}
            </button>
          ))}
        </div>
        <p className="font-mono text-xs text-muted-foreground tabular-nums" aria-live="polite">
          {M.inbox.unreadCount(unread)}
        </p>
      </div>

      {error && !data ? (
        <Alert variant="destructive">
          <AlertDescription className="flex items-center justify-between gap-3">
            {userMessage(error)}
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                void refetch().catch(() => undefined);
              }}
            >
              {M.verify.retry}
            </Button>
          </AlertDescription>
        </Alert>
      ) : loading && !data ? (
        <div className="flex flex-col gap-2">
          {Array.from({ length: 4 }, (_, i) => (
            <Skeleton key={i} className="h-20" />
          ))}
        </div>
      ) : items.length === 0 ? (
        <EmptyState
          icon={Inbox}
          title={filter === "unread" ? M.inbox.emptyUnread : M.inbox.emptyTitle}
          action={
            <Button variant="outline" size="sm" asChild>
              <Link to="/profile/notifications">{M.inbox.settings}</Link>
            </Button>
          }
        >
          {M.inbox.emptyBody}
        </EmptyState>
      ) : (
        <ol className="overflow-hidden rounded-lg border bg-card">
          {items.map((n) => (
            <InboxRow key={n.id} n={n} onRead={onRead} onDelete={onDelete} />
          ))}
        </ol>
      )}

      <Pager
        page={pager.page}
        hasNext={data?.notifications.pageInfo.hasNextPage ?? false}
        onPrev={pager.prev}
        onNext={() => {
          pager.next(data?.notifications.pageInfo.endCursor);
        }}
        label={M.inbox.pager}
      />
    </div>
  );
}
