import type {
  ConnectionTlsMode,
  DatabaseEngine,
  TargetPrivilegeStatus,
  TlsPinStatus,
} from "@/graphql/gen/graphql";
import { formatDateTime } from "@/lib/format";

import type { Tone } from "@/components/status/tag";

export const ENGINE_LABEL: Record<DatabaseEngine, string> = {
  POSTGRES: "PostgreSQL",
  MYSQL: "MySQL",
  MARIADB: "MariaDB",
};

export const ENGINES: readonly DatabaseEngine[] = ["POSTGRES", "MYSQL", "MARIADB"];

export function defaultPort(engine: DatabaseEngine): number {
  return engine === "POSTGRES" ? 5432 : 3306;
}

export const TLS_MODE: Record<
  ConnectionTlsMode,
  { label: string; short: string; description: string; strength: number }
> = {
  PINNED: {
    label: "Pinned TLS",
    short: "Pinned",
    description:
      "TLS verified against a certificate you confirmed (a self-signed server certificate or your private CA). The secure default.",
    strength: 2,
  },
  UNVERIFIED: {
    label: "TLS without verification",
    short: "Unverified",
    description:
      "Encrypted, but anyone on the network path can impersonate the server and read the credentials and data.",
    strength: 1,
  },
  DISABLED: {
    label: "No TLS",
    short: "Disabled",
    description:
      "Credentials and every byte of the backup travel in clear text. Only for a database reached through an SSH tunnel or on the same host.",
    strength: 0,
  },
};

export const TLS_MODES: readonly ConnectionTlsMode[] = ["PINNED", "UNVERIFIED", "DISABLED"];

/** True when moving from `from` to `to` weakens transport security. */
export function weakensTls(from: ConnectionTlsMode, to: ConnectionTlsMode): boolean {
  return TLS_MODE[to].strength < TLS_MODE[from].strength;
}

export function pinStatusMeta(
  status: TlsPinStatus,
  expiresAt?: string | null,
): { label: string; tone: Tone; detail?: string } {
  const until = expiresAt ? formatDateTime(expiresAt) : undefined;
  const detail = until ? `expires ${until}` : undefined;
  const withDetail = (m: { label: string; tone: Tone }) => (detail ? { ...m, detail } : m);
  switch (status) {
    case "NOT_REQUIRED":
      return { label: "No pin", tone: "neutral" };
    case "MISSING":
      return { label: "Pin missing", tone: "danger", detail: "confirm a certificate to use it" };
    case "VALID":
      return withDetail({ label: "Pin valid", tone: "success" });
    case "EXPIRES_WITHIN_30_DAYS":
      return withDetail({ label: "Expires < 30 days", tone: "warning" });
    case "EXPIRES_WITHIN_7_DAYS":
      return withDetail({ label: "Expires < 7 days", tone: "danger" });
    case "EXPIRED":
      return withDetail({ label: "Pin expired", tone: "danger" });
  }
}

export const PRIVILEGE_META: Record<
  TargetPrivilegeStatus,
  { label: string; tone: Tone; description: string }
> = {
  NOT_PRIVILEGED: {
    label: "Not privileged",
    tone: "success",
    description: "No privilege that allows host access or privilege escalation was found.",
  },
  PRIVILEGED: {
    label: "Privileged role",
    tone: "warning",
    description:
      "The role can reach the host or escalate privileges. SQL in a restored dump runs with these rights, so restores into this connection need an admin and an explicit acknowledgement.",
  },
  UNKNOWN: {
    label: "Privileges unknown",
    tone: "warning",
    description:
      "The privileges could not be determined completely and are treated as privileged: restores need an admin and an explicit acknowledgement.",
  },
};

const WARNINGS: Record<string, string> = {
  tls_staged_pin_in_use:
    "The server presented the staged backup certificate: confirm it to promote it to the pin.",
  tls_certificate_expires_within_30_days: "The server's certificate chain expires within 30 days.",
  tls_certificate_expires_within_7_days: "The server's certificate chain expires within 7 days.",
  non_innodb_tables:
    "Some tables do not use InnoDB: their dump is not taken from one consistent snapshot.",
  databases_with_unsupported_names_skipped:
    "Databases with names bekup cannot handle safely were skipped.",
  server_not_supported: "The server's version is older than every supported release.",
  no_allowlisted_client_for_server: "No allowlisted client image fits this server's version.",
  roles_without_passwords: "Some roles have no password set.",
};

/** A readable sentence for a stable warning code (the code itself when unknown). */
export function warningText(code: string): string {
  return WARNINGS[code] ?? code.replaceAll("_", " ");
}

/** A readable form of a privilege reason code (e.g. member_of:pg_execute_server_program). */
export function privilegeReason(code: string): string {
  const [kind, value] = code.split(/:(.*)/s, 2);
  switch (kind) {
    case "superuser":
      return "Superuser";
    case "createrole":
      return "May create roles (CREATEROLE)";
    case "replication":
      return "Replication role";
    case "bypassrls":
      return "Bypasses row-level security";
    case "member_of":
      return `Member of ${value ?? "a privileged role"}`;
    case "member_of_role_with":
      return `Member of a role with ${value ?? "privileges"}`;
    case "global":
      return `Global privilege ${value ?? ""}`.trim();
    case "proxy":
      return "Has PROXY rights";
    case "system_schema_write":
      return "Can write to the system schema";
    case "roles_granted":
      return "Roles are granted (not inspected)";
    case "detection_failed":
      return "The check could not complete";
    default:
      return code;
  }
}
