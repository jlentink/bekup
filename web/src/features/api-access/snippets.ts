/**
 * The ready-to-copy CI snippets of the API access panel (D-220). They are
 * the same text as docs/guides/external-api.md (snippets.test.ts compares
 * them): the guide's example base URL and job id are the only parts the UI
 * fills in. The token is always the `$BEKUP_TOKEN` variable of the CI
 * system and never part of a snippet (not even right after creating it).
 */

/** The example base URL and job id of the guide. */
export const GUIDE_BASE_URL = "https://backup.example.com/api/v1";
export const GUIDE_JOB_ID = "<job id>";
/** The start example of the guide names the job with a shell variable. */
export const GUIDE_JOB_VAR = "$JOB_ID";

/** The CI secret (environment variable) the token is stored in. */
export const TOKEN_VARIABLE = "BEKUP_TOKEN";

/** A job id (a UUID from the server): letters, digits and hyphens only. */
const JOB_ID = /^[0-9A-Za-z-]{1,64}$/;

/**
 * The external API's base URL on this bekup origin: the bundled proxy
 * routes `/api/v1` to `bekup external-api`.
 */
export function apiBaseUrl(origin: string = window.location.origin): string {
  // Only scheme, host and port (an origin never carries quotes or `$`).
  const u = new URL(origin);
  return `${u.protocol}//${u.host}/api/v1`;
}

/** A job id that may appear inside the double-quoted shell strings. */
function shellSafeJob(jobId: string): string {
  return JOB_ID.test(jobId) || jobId === GUIDE_JOB_ID || jobId === GUIDE_JOB_VAR
    ? jobId
    : GUIDE_JOB_ID;
}

/** (1) Start a backup: answers 202 with `requestId`. */
export function startSnippet(base: string, jobId: string): string {
  return [
    `curl -fsS -X POST -H "Authorization: Bearer $${TOKEN_VARIABLE}" \\`,
    `  "${base}/jobs/${shellSafeJob(jobId)}/runs"`,
  ].join("\n");
}

/** (2) Check the status of the request started by (1). */
export function statusSnippet(base: string): string {
  return [
    `curl -fsS -H "Authorization: Bearer $${TOKEN_VARIABLE}" \\`,
    `  "${base}/requests/$REQUEST_ID"`,
  ].join("\n");
}

/** Shows the address bekup sees for the caller (`clientIp`). */
export function selfSnippet(base: string): string {
  return `curl -fsS -H "Authorization: Bearer $${TOKEN_VARIABLE}" ${base}/self`;
}

/**
 * (3) The complete CI script: start, poll every 15 s until `done`, exit
 * non-zero unless `success`. Needs curl and jq.
 */
export function ciScript(base: string, jobId: string): string {
  return `#!/bin/sh
# Start a bekup backup and fail unless it succeeds (needs curl and jq).
# BEKUP_TOKEN comes from your CI secrets; never write the token here.
set -eu
BEKUP_URL="\${BEKUP_URL:-${base}}"
BEKUP_JOB_ID="\${BEKUP_JOB_ID:-${shellSafeJob(jobId)}}"
auth="Authorization: Bearer \${BEKUP_TOKEN}"
response=$(curl -fsS -X POST -H "$auth" \\
  -H "Idempotency-Key: \${CI_PIPELINE_ID:-$(date +%s)}" \\
  "\${BEKUP_URL}/jobs/\${BEKUP_JOB_ID}/runs")
request=$(printf '%s' "$response" | jq -r .requestId)
echo "bekup request \${request}"
while :; do
  sleep 15
  status=$(curl -fsS -H "$auth" "\${BEKUP_URL}/requests/\${request}")
  state=$(printf '%s' "$status" | jq -r .state)
  echo "bekup state: \${state}"
  if [ "$(printf '%s' "$status" | jq -r .done)" = "true" ]; then
    break
  fi
done
if [ "$(printf '%s' "$status" | jq -r .success)" != "true" ]; then
  echo "bekup backup did not succeed: $(printf '%s' "$status" | jq -c '{state, status, reason}')" >&2
  exit 1
fi
echo "bekup backup succeeded"
`;
}
