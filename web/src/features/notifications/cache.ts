import type { ApolloClient, ApolloCache } from "@apollo/client";

import { getFragmentData, makeFragmentData } from "@/graphql/gen";
import type { NotificationFieldsFragment } from "@/graphql/gen/graphql";

import { NotificationBellQuery, NotificationFields } from "./operations";

/** How many entries the bell shows. Keep in sync with NotificationBellQuery. */
export const BELL_LIMIT = 8;

const NOTIFICATION_QUERIES = new Set(["NotificationBell", "NotificationsInbox"]);

/**
 * Puts a notification from the live stream into the bell's cached list and
 * raises the unread count, once per notification (the stream may replay).
 * Returns false when it was already known.
 */
export function applyIncoming(cache: ApolloCache, n: NotificationFieldsFragment): boolean {
  let added = false;
  cache.updateQuery({ query: NotificationBellQuery }, (data) => {
    if (!data) return data;
    const known = data.notifications.edges.some(
      (e) => getFragmentData(NotificationFields, e.node).id === n.id,
    );
    if (known) return data;
    added = true;
    const edge = {
      __typename: "NotificationEdge" as const,
      cursor: "",
      node: makeFragmentData({ ...n, __typename: "Notification" as const }, NotificationFields),
    };
    return {
      ...data,
      unreadNotificationCount: data.unreadNotificationCount + (n.readAt ? 0 : 1),
      notifications: {
        ...data.notifications,
        edges: [edge, ...data.notifications.edges].slice(0, BELL_LIMIT),
      },
    };
  });
  return added;
}

/** Marks cached notifications read and stores the server's unread count. */
export function applyMarked(cache: ApolloCache, ids: readonly string[], unreadCount: number) {
  const now = new Date().toISOString();
  for (const id of ids) {
    const ref = cache.identify({ __typename: "Notification", id });
    if (!ref) continue;
    cache.modify({
      id: ref,
      fields: { readAt: (prev: string | null) => prev ?? now },
    });
  }
  cache.modify({ fields: { unreadNotificationCount: () => unreadCount } });
}

/** Reloads the notification lists that are on screen (bell, inbox). */
export function refetchNotificationLists(client: ApolloClient) {
  return client.refetchQueries({
    include: "active",
    onQueryUpdated: (query) => NOTIFICATION_QUERIES.has(query.queryName ?? ""),
  });
}
