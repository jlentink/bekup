import { useApolloClient, useMutation, useQuery } from "@apollo/client/react";
import { Bell, CheckCheck, Inbox, Settings2 } from "lucide-react";
import { useEffect, useState } from "react";
import { Link, useNavigate } from "react-router";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { getFragmentData } from "@/graphql/gen";
import type { NotificationFieldsFragment } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { cn } from "@/lib/utils";

import { applyMarked, refetchNotificationLists } from "./cache";
import { onNotificationArrival } from "./live";
import { notificationsMessages as M } from "./messages";
import { subjectPath } from "./model";
import { NotificationText, SeverityMark } from "./notification-item";
import {
  MarkNotificationsReadMutation,
  NotificationBellQuery,
  NotificationFields,
} from "./operations";

/**
 * The top-bar bell: unread badge and the latest entries (task 6.10). New
 * notifications arrive on the shell's account stream (D-197), which puts
 * them into the bell's cached list; the bell announces them.
 */
export function NotificationBell() {
  const client = useApolloClient();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [announcement, setAnnouncement] = useState("");
  const { data, error } = useQuery(NotificationBellQuery, { fetchPolicy: "cache-and-network" });
  const [markRead] = useMutation(MarkNotificationsReadMutation);

  const unread = data?.unreadNotificationCount ?? 0;
  const items = (data?.notifications.edges ?? []).map((e) =>
    getFragmentData(NotificationFields, e.node),
  );

  useEffect(
    () =>
      onNotificationArrival((n) => {
        setAnnouncement(`${n.title}. ${n.body}`);
        if (n.severity === "ERROR" || n.severity === "CRITICAL") {
          toast.error(n.title, { description: n.body, id: `notification-${n.id}` });
        }
      }),
    [],
  );

  const mark = async (ids: string[] | null) => {
    try {
      const r = await markRead({ variables: ids ? { ids } : { all: true } });
      const result = r.data?.markNotificationsRead;
      if (!result) return;
      if (ids) applyMarked(client.cache, ids, result.unreadCount);
      else await refetchNotificationLists(client);
    } catch (err) {
      toast.error(userMessage(err));
    }
  };

  const openItem = (n: NotificationFieldsFragment) => {
    if (n.readAt === null) void mark([n.id]);
    const path = subjectPath(n.link);
    void navigate(path ?? "/notifications");
  };

  return (
    <>
      <span className="sr-only" role="status" aria-live="polite">
        {announcement}
      </span>
      <DropdownMenu open={open} onOpenChange={setOpen}>
        <Tooltip>
          <TooltipTrigger asChild>
            <DropdownMenuTrigger asChild>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={M.bell.label(unread)}
                className="relative"
                data-slot="notification-bell"
              >
                <Bell aria-hidden="true" />
                {unread > 0 ? (
                  <span
                    aria-hidden="true"
                    data-testid="bell-badge"
                    className="absolute -top-0.5 -right-0.5 inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-primary px-1 font-mono text-[0.625rem] leading-none font-semibold text-primary-foreground tabular-nums ring-2 ring-background"
                  >
                    {M.bell.unread(unread)}
                  </span>
                ) : null}
              </Button>
            </DropdownMenuTrigger>
          </TooltipTrigger>
          <TooltipContent>{M.bell.heading}</TooltipContent>
        </Tooltip>
        <DropdownMenuContent
          align="end"
          sideOffset={8}
          className="w-[min(24rem,calc(100vw-2rem))] p-0"
          aria-label={M.bell.heading}
        >
          <div className="flex items-end justify-between gap-3 border-b border-rule px-4 pt-3.5 pb-3">
            <div className="flex flex-col gap-0.5">
              <p className="eyebrow">{M.inbox.eyebrow}</p>
              <p className="font-display text-xl leading-none tracking-tight">{M.bell.heading}</p>
            </div>
            <span className="font-mono text-2xs text-muted-foreground tabular-nums">
              {M.inbox.unreadCount(unread)}
            </span>
          </div>
          <div className="max-h-[min(26rem,60dvh)] overflow-y-auto py-1">
            {error && !data ? (
              <p className="px-4 py-6 text-sm text-muted-foreground">{M.bell.loadFailed}</p>
            ) : items.length === 0 ? (
              <p className="px-4 py-6 text-sm text-muted-foreground">{M.bell.empty}</p>
            ) : (
              items.map((n) => (
                <DropdownMenuItem
                  key={n.id}
                  data-testid="bell-item"
                  data-unread={n.readAt === null}
                  className={cn(
                    "mx-1 flex items-start gap-3 rounded-sm px-3 py-2.5",
                    n.readAt === null && "bg-accent/40",
                  )}
                  onSelect={() => {
                    openItem(n);
                  }}
                >
                  <SeverityMark severity={n.severity} />
                  <NotificationText n={n} clamp />
                </DropdownMenuItem>
              ))
            )}
          </div>
          <DropdownMenuSeparator className="my-0" />
          <div className="grid grid-cols-[1fr_auto_auto] gap-0.5 p-1">
            <DropdownMenuItem asChild>
              <Link to="/notifications">
                <Inbox aria-hidden="true" /> {M.bell.viewAll}
              </Link>
            </DropdownMenuItem>
            <DropdownMenuItem
              disabled={unread === 0}
              onSelect={() => {
                void mark(null);
              }}
            >
              <CheckCheck aria-hidden="true" /> {M.bell.markAll}
            </DropdownMenuItem>
            <DropdownMenuItem asChild>
              <Link
                to="/profile/notifications"
                aria-label={M.bell.settings}
                title={M.bell.settings}
              >
                <Settings2 aria-hidden="true" />
              </Link>
            </DropdownMenuItem>
          </div>
        </DropdownMenuContent>
      </DropdownMenu>
    </>
  );
}
