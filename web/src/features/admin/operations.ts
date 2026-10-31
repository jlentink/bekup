import { graphql } from "@/graphql/gen";

/* ---- SMTP (D-178, D-180) ---- */

export const SmtpSettingsFields = graphql(`
  fragment SmtpSettingsFields on SmtpSettings {
    configured
    enabled
    host
    port
    mode
    username
    hasPassword
    fromAddress
    fromName
    caBundlePem
    connectTimeoutSeconds
    sendTimeoutSeconds
    mayBeUnencrypted
    updatedAt
  }
`);

export const SmtpSettingsQuery = graphql(`
  query AdminSmtpSettings {
    smtpSettings {
      ...SmtpSettingsFields
    }
  }
`);

export const UpdateSmtpSettingsMutation = graphql(`
  mutation AdminUpdateSmtpSettings($input: UpdateSmtpSettingsInput!) {
    updateSmtpSettings(input: $input) {
      ...SmtpSettingsFields
    }
  }
`);

export const SendTestEmailMutation = graphql(`
  mutation AdminSendTestEmail($to: String!) {
    sendTestEmail(to: $to) {
      ok
      code
      message
      detail
      encrypted
      tlsVersion
    }
  }
`);

/* ---- Shared channels (D-177, D-191) ---- */

export const SharedChannelFields = graphql(`
  fragment SharedChannelFields on SharedChannel {
    id
    name
    type
    addresses
    displayUrl
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
    attachments {
      jobId
      jobName
      groups
      events
    }
    createdAt
    updatedAt
  }
`);

export const SharedChannelsQuery = graphql(`
  query AdminSharedChannels {
    sharedChannels {
      ...SharedChannelFields
    }
  }
`);

export const NotificationCatalogQuery = graphql(`
  query AdminNotificationCatalog {
    notificationCatalog {
      group
      label
      events {
        type
        label
        group
        mandatory
        perJob
        severity
      }
    }
  }
`);

export const CreateSharedChannelMutation = graphql(`
  mutation AdminCreateSharedChannel($input: CreateSharedChannelInput!) {
    createSharedChannel(input: $input) {
      channel {
        ...SharedChannelFields
      }
      signingKey
    }
  }
`);

export const UpdateSharedChannelMutation = graphql(`
  mutation AdminUpdateSharedChannel($id: UUID!, $input: UpdateSharedChannelInput!) {
    updateSharedChannel(id: $id, input: $input) {
      ...SharedChannelFields
    }
  }
`);

export const RotateSharedChannelKeyMutation = graphql(`
  mutation AdminRotateSharedChannelKey($id: UUID!) {
    rotateSharedChannelKey(id: $id) {
      channel {
        ...SharedChannelFields
      }
      signingKey
    }
  }
`);

export const DeleteSharedChannelMutation = graphql(`
  mutation AdminDeleteSharedChannel($id: UUID!) {
    deleteSharedChannel(id: $id)
  }
`);

export const EnableSharedChannelMutation = graphql(`
  mutation AdminEnableSharedChannel($id: UUID!) {
    enableSharedChannel(id: $id) {
      ...SharedChannelFields
    }
  }
`);

export const DisableSharedChannelMutation = graphql(`
  mutation AdminDisableSharedChannel($id: UUID!) {
    disableSharedChannel(id: $id) {
      ...SharedChannelFields
    }
  }
`);

export const TestSharedChannelMutation = graphql(`
  mutation AdminTestSharedChannel($id: UUID!) {
    testSharedChannel(id: $id) {
      ok
      code
      message
      statusCode
    }
  }
`);

export const AttachSharedChannelMutation = graphql(`
  mutation AdminAttachSharedChannel(
    $jobId: UUID!
    $channelId: UUID!
    $filter: NotificationFilterInput
  ) {
    attachSharedChannel(jobId: $jobId, channelId: $channelId, filter: $filter) {
      ...SharedChannelFields
    }
  }
`);

export const DetachSharedChannelMutation = graphql(`
  mutation AdminDetachSharedChannel($jobId: UUID!, $channelId: UUID!) {
    detachSharedChannel(jobId: $jobId, channelId: $channelId) {
      ...SharedChannelFields
    }
  }
`);

/** Jobs for pickers and the recovery page (admins see every job). */
export const JobsQuery = graphql(`
  query AdminJobs($filter: JobFilter, $first: Int, $after: String) {
    jobs(filter: $filter, first: $first, after: $after) {
      edges {
        cursor
        node {
          id
          name
          slug
          enabled
          encryption {
            configured
            mode
            optedOut
            passphrase {
              version
              createdAt
            }
          }
        }
      }
      pageInfo {
        hasNextPage
        endCursor
      }
    }
  }
`);

/* ---- Audit log (D-101) ---- */

export const AuditLogQuery = graphql(`
  query AdminAuditLog($filter: AuditLogFilter, $first: Int, $after: String) {
    auditLog(filter: $filter, first: $first, after: $after) {
      edges {
        cursor
        node {
          id
          ts
          actorKind
          actorUserId
          action
          targetType
          targetId
          ip
          details
          hash
        }
      }
      pageInfo {
        hasNextPage
        endCursor
      }
    }
  }
`);

/** The chain status (D-196): latest periodic verification, head, signed head. */
export const AuditChainQuery = graphql(`
  query AdminAuditChain {
    auditChainStatus {
      lastVerification {
        checkedAt
        ok
        full
        finding
        verifiedThroughId
      }
      headId
      headHash
      headAt
      latestSignedHead {
        chainId
        signedAt
        keyId
      }
    }
  }
`);

/* ---- Security alerts (D-190, D-196): the admin's own inbox, filtered server-side ---- */

/** The reference list of security.system alert kinds (server-owned). */
export const SecurityAlertTypesQuery = graphql(`
  query AdminSecurityAlertTypes {
    securityAlertTypes {
      kind
      severity
      description
      repeat
    }
  }
`);

export const SecurityInboxQuery = graphql(`
  query AdminSecurityInbox($filter: NotificationInboxFilter, $first: Int, $after: String) {
    notifications(filter: $filter, first: $first, after: $after) {
      edges {
        cursor
        node {
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
      }
      pageInfo {
        hasNextPage
        endCursor
      }
    }
  }
`);

/* ---- SSRF allowlist (D-120, D-196) ---- */

export const SsrfAllowlistFields = graphql(`
  fragment SsrfAllowlistFields on SsrfAllowlist {
    cidrEntries {
      value
      scopes
    }
    hostEntries {
      value
      scopes
    }
    configScopes
    defaultScopes
    personalWebhooksWidened
    configCidrs
    configHosts
    loopbackAllowed
    ignoredEntries
    maxEntries
    updatedAt
  }
`);

export const SsrfAllowlistQuery = graphql(`
  query AdminSsrfAllowlist {
    ssrfAllowlist {
      ...SsrfAllowlistFields
    }
  }
`);

export const UpdateSsrfAllowlistMutation = graphql(`
  mutation AdminUpdateSsrfAllowlist($input: SsrfAllowlistInput!) {
    updateSsrfAllowlist(input: $input) {
      ...SsrfAllowlistFields
    }
  }
`);

/* ---- Runtime config reload (task 6.13, D-200): key names only, never values ---- */

export const ConfigProcessResultFields = graphql(`
  fragment ConfigProcessResultFields on ConfigProcessResult {
    instanceId
    kind
    hostname
    version
    processStartedAt
    finishedAt
    requestId
    trigger
    listening
    status
    errorKind
    appliedKeys
    restartKeys
    errorKeys
    weakenedKeys
  }
`);

export const ConfigReloadRequestFields = graphql(`
  fragment ConfigReloadRequestFields on ConfigReloadRequest {
    id
    requestedAt
    trigger
    listeningProcesses
    expectedProcesses
    results {
      ...ConfigProcessResultFields
    }
  }
`);

export const ConfigStatusQuery = graphql(`
  query AdminConfigStatus {
    configStatus {
      database {
        mode
        tlsMode
        insecureTransport
        singleAccount
      }
      image {
        following
        setupVersion
        version
      }
      liveKeys
      listeningProcesses
      processes {
        ...ConfigProcessResultFields
      }
      lastRequest {
        ...ConfigReloadRequestFields
      }
    }
  }
`);

export const ConfigReloadRequestQuery = graphql(`
  query AdminConfigReloadRequest($id: UUID!) {
    configReloadRequest(id: $id) {
      ...ConfigReloadRequestFields
    }
  }
`);

export const ReloadConfigMutation = graphql(`
  mutation AdminReloadConfig {
    reloadConfig {
      ...ConfigReloadRequestFields
    }
  }
`);
