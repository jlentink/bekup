import { graphql } from "@/graphql/gen";

/** One inbox entry (bell, inbox page, the account stream). */
export const NotificationFields = graphql(`
  fragment NotificationFields on Notification {
    id
    type
    group
    severity
    title
    body
    link
    createdAt
    readAt
  }
`);

/** The bell: the unread count and the latest entries in one round trip. */
export const NotificationBellQuery = graphql(`
  query NotificationBell {
    unreadNotificationCount
    notifications(first: 8) {
      edges {
        cursor
        node {
          ...NotificationFields
        }
      }
      pageInfo {
        hasNextPage
        endCursor
      }
    }
  }
`);

export const NotificationsInboxQuery = graphql(`
  query NotificationsInbox($unreadOnly: Boolean, $first: Int, $after: String) {
    notifications(unreadOnly: $unreadOnly, first: $first, after: $after) {
      edges {
        cursor
        node {
          ...NotificationFields
        }
      }
      pageInfo {
        hasNextPage
        endCursor
      }
    }
    unreadNotificationCount
  }
`);

export const MarkNotificationsReadMutation = graphql(`
  mutation MarkNotificationsRead($ids: [UUID!], $all: Boolean) {
    markNotificationsRead(ids: $ids, all: $all) {
      marked
      unreadCount
    }
  }
`);

export const DeleteNotificationMutation = graphql(`
  mutation DeleteNotification($id: UUID!) {
    deleteNotification(id: $id)
  }
`);

/* ---- Preferences (D-161, D-191) ---- */

export const NotificationEventPreferenceFields = graphql(`
  fragment NotificationEventPreferenceFields on NotificationEventPreference {
    event {
      type
      label
      group
      problem
      mandatory
      priority
      severity
      perJob
    }
    channels {
      channel
      enabled
      source
      locked
    }
    jobOverrides {
      jobId
      jobName
      channel
      enabled
    }
  }
`);

export const NotificationPreferencesFields = graphql(`
  fragment NotificationPreferencesFields on NotificationPreferences {
    emailVerified
    activeWebhooks
    groups {
      group
      label
      events {
        ...NotificationEventPreferenceFields
      }
    }
  }
`);

export const MyNotificationPreferencesQuery = graphql(`
  query MyNotificationPreferences {
    myNotificationPreferences {
      ...NotificationPreferencesFields
    }
  }
`);

export const SetNotificationPreferenceMutation = graphql(`
  mutation SetNotificationPreference(
    $event: String!
    $channel: NotificationChannelType!
    $enabled: Boolean!
    $jobId: UUID
  ) {
    setNotificationPreference(event: $event, channel: $channel, enabled: $enabled, jobId: $jobId) {
      ...NotificationEventPreferenceFields
    }
  }
`);

export const ResetNotificationPreferencesMutation = graphql(`
  mutation ResetNotificationPreferences($jobId: UUID) {
    resetNotificationPreferences(jobId: $jobId) {
      ...NotificationPreferencesFields
    }
  }
`);

/** Jobs for the per-job override picker (the caller's visible jobs). */
export const NotificationJobOptionsQuery = graphql(`
  query NotificationJobOptions($search: String) {
    jobs(filter: { search: $search }, first: 50) {
      edges {
        node {
          id
          name
        }
      }
    }
  }
`);

/* ---- Personal channels ---- */

export const PersonalWebhookFields = graphql(`
  fragment PersonalWebhookFields on PersonalWebhook {
    id
    label
    displayUrl
    createdAt
    state {
      enabled
      disabledAt
      disabledReason
      disabledMessage
      consecutiveFailures
      permanentFailures
      lastFailureAt
      lastFailureCode
    }
  }
`);

export const MyNotificationChannelsQuery = graphql(`
  query MyNotificationChannels {
    myNotificationChannels {
      email
      emailVerified
      emailVerifiedAt
      maxWebhooks
      webhooks {
        ...PersonalWebhookFields
      }
    }
  }
`);

/*
 * addMyWebhook and rotateMyWebhookKey return the signing key exactly once:
 * they run with fetchPolicy "no-cache" so the key never enters the Apollo
 * cache and nothing can render it again after its dialog is closed.
 */
export const AddMyWebhookMutation = graphql(`
  mutation AddMyWebhook($url: String!, $label: String!) {
    addMyWebhook(url: $url, label: $label) {
      webhook {
        id
        label
        displayUrl
      }
      signingKey
    }
  }
`);

export const RotateMyWebhookKeyMutation = graphql(`
  mutation RotateMyWebhookKey($id: UUID!) {
    rotateMyWebhookKey(id: $id) {
      webhook {
        id
        label
        displayUrl
      }
      signingKey
    }
  }
`);

export const DeleteMyWebhookMutation = graphql(`
  mutation DeleteMyWebhook($id: UUID!) {
    deleteMyWebhook(id: $id)
  }
`);

export const TestMyWebhookMutation = graphql(`
  mutation TestMyWebhook($id: UUID!) {
    testMyWebhook(id: $id) {
      ok
      code
    }
  }
`);

export const ReenableMyWebhookMutation = graphql(`
  mutation ReenableMyWebhook($id: UUID!) {
    reenableMyWebhook(id: $id) {
      ...PersonalWebhookFields
    }
  }
`);

export const RequestEmailVerificationMutation = graphql(`
  mutation RequestEmailVerification {
    requestEmailVerification {
      sentTo
      expiresAt
    }
  }
`);

export const VerifyEmailMutation = graphql(`
  mutation VerifyEmail($code: String!) {
    verifyEmail(code: $code) {
      id
      emailVerified
      emailVerifiedAt
    }
  }
`);
