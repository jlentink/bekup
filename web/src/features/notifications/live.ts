import type { ApolloClient } from "@apollo/client";

import type { NotificationFieldsFragment } from "@/graphql/gen/graphql";

import { applyIncoming } from "./cache";

type Arrival = (n: NotificationFieldsFragment) => void;

const arrivals = new Set<Arrival>();

/**
 * Listens for notifications that arrived on the account stream and were
 * new to this tab (the bell announces them). Returns the unsubscribe
 * function.
 */
export function onNotificationArrival(listener: Arrival): () => void {
  arrivals.add(listener);
  return () => {
    arrivals.delete(listener);
  };
}

/**
 * Handles a notification from the account stream (D-197; the shell's
 * AccountStream): puts it into the bell's cached list and count once,
 * reloads the inbox page when it is shown and tells the bell.
 */
export function receiveNotification(client: ApolloClient, n: NotificationFieldsFragment): void {
  if (!applyIncoming(client.cache, n)) return;
  for (const l of [...arrivals]) l(n);
  void client
    .refetchQueries({
      include: "active",
      onQueryUpdated: (q) => q.queryName === "NotificationsInbox",
    })
    .catch(() => undefined);
}
