import { useApolloClient, useSubscription } from "@apollo/client/react";

import { useSessionUser } from "@/features/auth/use-session-user";
import { receiveNotification } from "@/features/notifications/live";
import { NotificationFields } from "@/features/notifications/operations";
import { getFragmentData } from "@/graphql/gen";
import { sharedStream } from "@/lib/apollo";
import { emitAuthEvent } from "@/lib/auth-events";

import { AccountEventsSubscription } from "./operations";

/**
 * The one account stream of the browser (D-197): shared across tabs (one
 * tab holds it, the others receive its events), keyed on the account.
 *
 * Session changes: a change to this session asks the server whether it
 * still holds (session-check: a password change revokes the old session
 * but this browser has a new one); other changes refresh the session list
 * if it is shown. Notifications go into the bell's list and count.
 */
export function AccountStream() {
  const client = useApolloClient();
  const user = useSessionUser();
  useSubscription(AccountEventsSubscription, {
    context: sharedStream(`account:${user.id}`),
    onData: ({ data }) => {
      const ev = data.data?.accountEvents;
      if (!ev) return;
      if (ev.notification) {
        receiveNotification(client, getFragmentData(NotificationFields, ev.notification));
        return;
      }
      const s = ev.session;
      if (!s) return;
      if (s.current && s.kind !== "ROTATED") {
        emitAuthEvent("session-check");
        return;
      }
      // Only when the list is on screen (no warning for an inactive query).
      void client.refetchQueries({
        include: "active",
        onQueryUpdated: (query) => query.queryName === "MySessions",
      });
    },
  });
  return null;
}
