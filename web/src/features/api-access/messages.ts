/** Strings of the API access UI (D-220). */

const plural = (n: number, one: string, many: string) => `${String(n)} ${n === 1 ? one : many}`;

export const T = {
  panel: {
    title: "API access",
    lead: "Let a CI pipeline start this job's backup and wait for the result, for example before a deployment migrates the database. Each token is shown once, expires and can be revoked at any time.",
    create: "Create API token",
    empty: "No API tokens for this job yet.",
    loadFailed: "The API tokens could not be loaded.",
    examples: "Use it from CI",
    examplesLead: (variable: string) =>
      `Store the token as a CI secret named ${variable}. The examples read it from that variable; never paste the token into a script or URL.`,
    revokedHeading: (n: number) => `Revoked or expired (${String(n)})`,
  },
  page: {
    eyebrow: "Administration · External API",
    title: "API access",
    lead: "Every external API token of this instance: which jobs it may start, where it was last used from and which addresses it is limited to. Revoke tokens you do not recognize.",
    empty: "No API tokens yet",
    emptyLead:
      "Admins and backup owners create tokens on a job's page (API access) or here. A token lets a CI pipeline start backups of its jobs and follow them.",
    guide: "External API guide",
  },
  list: {
    label: "API tokens",
    prefix: "Token",
    created: (at: string, by: string | null) => (by ? `Created ${at} by ${by}` : `Created ${at}`),
    createdByYou: "you",
    expiry: "Expiry",
    expires: (at: string) => `Expires ${at}`,
    expired: (at: string) => `Expired ${at}`,
    revoked: (at: string) => `Revoked ${at}`,
    states: {
      active: "Active",
      expiring: "Expires soon",
      expired: "Expired",
      revoked: "Revoked",
    },
    expiringTitle: (days: number) =>
      days <= 0
        ? "Expires within a day: create a new token and update the CI secret."
        : `Expires in ${plural(days, "day", "days")}: create a new token and update the CI secret.`,
    jobs: "Jobs",
    otherJobs: (n: number) =>
      n === 0 ? "Only this job" : `+ ${plural(n, "other job", "other jobs")}`,
    hiddenJobs: (n: number) => `+ ${plural(n, "job", "jobs")} you cannot see`,
    deletedJob: "deleted",
    addresses: "Allowed from",
    lastUse: "Last seen",
    never: "Never used",
    seen: (when: string, ip: string | null | undefined) => (ip ? `${when} from ${ip}` : when),
    refused: "Refused: address outside the allowlist",
    refusedTitle:
      "The last attempt came from an address outside the token's allowlist and was refused. If you do not recognize the address, revoke the token.",
    actions: (name: string) => `Actions for ${name}`,
    editAddresses: "Edit addresses",
    revoke: "Revoke",
    revokeTitle: (name: string) => `Revoke ${name}?`,
    revokeLead:
      "The token stops working at once: the next CI call gets 401. This cannot be undone; create a new token if you need access again.",
    revokeConfirm: "Revoke token",
    revokedToast: "API token revoked.",
  },
  addresses: {
    legend: "Client addresses",
    label: "Allowed networks",
    hint: (max: number) =>
      `One IPv4 or IPv6 address or network per line (for example 203.0.113.7 or 203.0.113.0/24), at most ${String(max)}. Calls from anywhere else are refused, and the admins and the creator are alerted.`,
    selfHint: "To find the address bekup sees for your runner, call",
    selfHintAfter: "with any working token and read clientIp.",
    any: "Allow from any address",
    anyWarning:
      "Without an allowlist, anyone who obtains the token can start this job's backups from anywhere. The token keeps a permanent warning badge, and the admins are alerted. Prefer CI runners with fixed addresses.",
    hostedRunners:
      "Hosted CI runners (such as GitHub-hosted runners) use large, changing address ranges: an allowlist of those keeps out little.",
  },
  create: {
    title: "Create an API token",
    lead: "The token can start backups of the selected jobs through the external API. It is shown once after you confirm your identity.",
    name: "Name",
    nameHint: "Where it is used, e.g. “deploy pipeline (GitLab)”.",
    nameMissing: "Enter a name (1 to 100 characters).",
    jobs: "Jobs",
    jobsHint: "The token can start backups of these jobs only.",
    jobsFilter: "Search jobs",
    jobsTruncated: "More jobs match: search by name to find the others.",
    jobsNoMatch: "No job matches the search.",
    jobsMissing: "Choose at least one job.",
    jobsLoading: "Loading jobs…",
    noJobs: "No jobs you may use.",
    expiry: "Expires after",
    days: (n: number) => `${String(n)} days`,
    expiryHint: "Required. Rotate the token before it expires.",
    submit: "Create token",
  },
  edit: {
    title: (name: string) => `Addresses of ${name}`,
    lead: "Changes apply to the next call and are audited; allowing any address alerts the admins.",
    submit: "Save addresses",
    saved: "Addresses saved.",
  },
  showOnce: {
    eyebrow: "Shown once",
    title: "Save the API token",
    lead: (variable: string) =>
      `Store it now as a CI secret named ${variable}. bekup keeps only a hash: you won't see this token again, and a lost token cannot be recovered (create a new one).`,
    tokenLabel: "API token",
    copy: "Copy token",
    copied: "Token copied to the clipboard.",
    copyFailed: "Copying failed. Select the token and copy it by hand.",
    acknowledge: "I stored the token as a CI secret; I won't see it again",
    done: "Done",
  },
  disabled: {
    title: "The external API is not enabled on this server",
    lead: "CI pipelines cannot call it yet. An administrator enables it on the host with",
    command: "docker compose run --rm setup upgrade --external-api",
    after:
      " and the steps it prints. Tokens created now work once it runs; the examples appear here then.",
    guide: "External API guide",
  },
  examples: {
    region: "curl examples",
    start: "1. Start a backup",
    startLead: "Answers 202 with a requestId. A run of the job already in progress gives 409.",
    status: "2. Check the status",
    statusLead:
      "Set REQUEST_ID to the requestId from step 1. Poll until done is true: state shows where the run is (pending, queued, running, retrying, then success, partial, failed, canceled or skipped), and success is true only when every database was backed up (partial is not a success).",
    script: "3. Complete CI script",
    scriptLead:
      "Starts the backup, polls every 15 seconds until done and exits non-zero unless success. Needs curl and jq.",
    copy: (what: string) => `Copy: ${what}`,
    copied: "Copied to the clipboard.",
    copyFailed: "Copying failed. Select the text and copy it by hand.",
  },
} as const;
