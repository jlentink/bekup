import { graphql } from "@/graphql/gen";

/**
 * The account stream (D-197): own session changes and new inbox
 * notifications in one subscription, shared by all tabs of the browser.
 */
export const AccountEventsSubscription = graphql(`
  subscription AccountEvents {
    accountEvents {
      session {
        kind
        sessionId
        current
      }
      notification {
        ...NotificationFields
      }
    }
  }
`);
