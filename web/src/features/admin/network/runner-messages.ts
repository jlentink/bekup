import type {
  RunnerDeniedCategory,
  RunnerTargetWarning,
  RunnerTargetsKey,
} from "@/graphql/gen/graphql";

/** Strings of the runner part of the Network page (D-223). */
export const RT = {
  title: "Database servers (runner)",
  lead: "Database servers and SSH bastions are reached by the runner, never by bekup itself. The runner allows only what the [targets] section of its own runner.toml lists; everything else is refused.",
  readOnly: "Read-only",
  editNote:
    "Edited in runner.toml [targets] on the runner host; restart the runner to apply. The runner has no access to bekup's database, so this page cannot change it.",
  guide: "Runner target allowlist",
  loading: "Asking the runner…",
  unavailableTitle: "The runner's list is not available right now",
  unavailableLead:
    "The rest of this page works as usual. The runner keeps enforcing its list; only this view is missing.",
  retry: "Ask again",
  allowTitle: "Allowed",
  allowLead:
    "Database servers and bastions the runner may connect to: an address in one of the networks, or one of the host names.",
  denyTitle: "Refused first",
  denyLead: "Checked before the allowed lists: a match here is refused even if it is also allowed.",
  cidrs: "Networks (allow_cidrs)",
  hosts: "Host names (allow_hosts)",
  denyCidrs: "Networks (deny_cidrs)",
  denyHosts: "Host names (deny_hosts)",
  noAllowCidrs:
    "No CIDR allowed — every database server is refused unless a host name below allows it.",
  noAllowHosts: "No host name allowed.",
  nothingAllowedTag: "Refuses everything",
  nothingAllowed:
    "Nothing is allowed: the runner refuses every database server and SSH bastion until runner.toml lists them.",
  noDenyCidrs: "No networks refused beyond the always-refused ranges.",
  noDenyHosts: "No host names refused.",
  bastionsTitle: "SSH bastions",
  bastionsLead:
    "What each bastion may forward to (bastion_targets). The bastion itself must also be allowed above.",
  bastion: "Bastion",
  bastionTargets: "May forward to",
  bastionsCaption: "SSH bastions and the targets each may forward to",
  noBastions:
    "No bastion is restricted: an allowed SSH bastion may forward to any host it reaches; only the refused lists apply behind it.",
  ownTitle: "The runner's own networks",
  ownLead: "Always refused, so a connection can never point back at the runner or another run.",
  pool: "Run subnet pool (docker.run_subnet_pool)",
  poolNote: "Every run gets its own network inside it.",
  control: "Control network and the runner's addresses",
  controlPending:
    "Not known yet: the runner has not finished connecting to Docker. Ask again in a moment.",
  noPool: "Not reported.",
  deniedTitle: "Always refused",
  deniedLead:
    "No runner.toml entry can open these. Addresses inside other Docker networks on the runner host are refused too, unless an allow_cidrs entry inside that network allows them.",
  categories: {
    LOOPBACK: "Loopback",
    UNSPECIFIED: "Unspecified and “this network”",
    LINK_LOCAL: "Link-local",
    CLOUD_METADATA: "Cloud metadata endpoints",
    MULTICAST_BROADCAST: "Multicast and broadcast",
    RESERVED: "Reserved",
    IPV4_IN_IPV6: "IPv4-compatible and IPv4-translated IPv6",
    TEREDO: "Teredo",
    EMBEDDED_DENIED_IPV4: "NAT64 and 6to4 forms of an always-refused IPv4 address",
    NON_GLOBAL_IPV6: "Other IPv6 outside global unicast (2000::/3)",
  } satisfies Record<RunnerDeniedCategory, string>,
  warningsTitle: "Runner warnings",
  warnings: {
    EMPTY_ALLOWLIST:
      "allow_cidrs and allow_hosts are empty: the runner refuses every database server and SSH bastion.",
    BASTIONS_UNRESTRICTED:
      "bastion_targets is empty: an allowed SSH bastion may forward to any host it reaches. List each bastion's databases in bastion_targets.",
  } satisfies Record<RunnerTargetWarning, string>,
  envTitle: "Set by environment variables",
  envLead: (keys: string) =>
    `These keys come from BEKUP_TARGETS_* variables of the runner, not from runner.toml: ${keys}.`,
  keys: {
    ALLOW_CIDRS: "allow_cidrs",
    ALLOW_HOSTS: "allow_hosts",
    DENY_CIDRS: "deny_cidrs",
    DENY_HOSTS: "deny_hosts",
    BASTION_TARGETS: "bastion_targets",
  } satisfies Record<RunnerTargetsKey, string>,
  truncated: "The runner reports at most 256 entries per list; runner.toml holds more.",
} as const;
