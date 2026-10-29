/** Links into the project documentation on GitHub. */
export const DOCS_BASE = "https://github.com/jlentink/bekup/blob/main/docs";

export const docs = {
  smtp: `${DOCS_BASE}/guides/notifications.md#e-mail-smtp`,
  sharedChannels: `${DOCS_BASE}/guides/notifications.md#shared-channels-admins`,
  webhookSignature: `${DOCS_BASE}/guides/notifications.md#verifying-the-signature`,
  securityAlerts: `${DOCS_BASE}/guides/notifications.md#security-alerts`,
  audit: `${DOCS_BASE}/security/audit.md`,
  netguard: `${DOCS_BASE}/configuration.md#netguard`,
  recovery: `${DOCS_BASE}/recovery.md`,
  configReload: `${DOCS_BASE}/configuration.md#reloading`,
} as const;
