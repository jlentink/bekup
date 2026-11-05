/** Links into the project documentation on GitHub. */
export const DOCS_BASE = "https://github.com/jlentink/bekup/blob/main/docs";

export const docs = {
  home: `${DOCS_BASE}/README.md`,
  changelog: "https://github.com/jlentink/bekup/blob/main/CHANGELOG.md",
  smtp: `${DOCS_BASE}/guides/notifications.md#e-mail-smtp`,
  sharedChannels: `${DOCS_BASE}/guides/notifications.md#shared-channels-admins`,
  webhookSignature: `${DOCS_BASE}/guides/notifications.md#verifying-the-signature`,
  securityAlerts: `${DOCS_BASE}/guides/notifications.md#security-alerts`,
  audit: `${DOCS_BASE}/security/audit.md`,
  netguard: `${DOCS_BASE}/configuration.md#netguard`,
  runnerTargets: `${DOCS_BASE}/security/target-allowlist.md#where-it-is-shown`,
  recovery: `${DOCS_BASE}/recovery.md`,
  configReload: `${DOCS_BASE}/configuration.md#reloading`,
  externalApi: `${DOCS_BASE}/guides/external-api.md`,
} as const;
