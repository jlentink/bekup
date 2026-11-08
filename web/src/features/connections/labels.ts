import type {
  ConnectionTlsMode,
  DatabaseEngine,
  TargetPrivilegeStatus,
  TlsPinStatus,
} from "@/graphql/gen/graphql";
import { formatDateTime } from "@/lib/format";
import { defineMessages } from "@/lib/i18n";

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

const L = defineMessages({
  en: {
    tls: {
      PINNED: {
        label: "Pinned TLS",
        short: "Pinned",
        description:
          "TLS verified against a certificate you confirmed (a self-signed server certificate or your private CA). The secure default.",
      },
      UNVERIFIED: {
        label: "TLS without verification",
        short: "Unverified",
        description:
          "Encrypted, but anyone on the network path can impersonate the server and read the credentials and data.",
      },
      DISABLED: {
        label: "No TLS",
        short: "Disabled",
        description:
          "Credentials and every byte of the backup travel in clear text. Only for a database reached through an SSH tunnel or on the same host.",
      },
    },
    pin: {
      expires: (until: string) => `expires ${until}`,
      none: "No pin",
      missing: "Pin missing",
      missingDetail: "confirm a certificate to use it",
      valid: "Pin valid",
      expires30: "Expires < 30 days",
      expires7: "Expires < 7 days",
      expired: "Pin expired",
    },
    privilege: {
      NOT_PRIVILEGED: {
        label: "Not privileged",
        description: "No privilege that allows host access or privilege escalation was found.",
      },
      PRIVILEGED: {
        label: "Privileged role",
        description:
          "The role can reach the host or escalate privileges. SQL in a restored dump runs with these rights, so restores into this connection need an admin and an explicit acknowledgement.",
      },
      UNKNOWN: {
        label: "Privileges unknown",
        description:
          "The privileges could not be determined completely and are treated as privileged: restores need an admin and an explicit acknowledgement.",
      },
    },
    warnings: {
      tls_staged_pin_in_use:
        "The server presented the staged backup certificate: confirm it to promote it to the pin.",
      tls_certificate_expires_within_30_days:
        "The server's certificate chain expires within 30 days.",
      tls_certificate_expires_within_7_days:
        "The server's certificate chain expires within 7 days.",
      non_innodb_tables:
        "Some tables do not use InnoDB: their dump is not taken from one consistent snapshot.",
      databases_with_unsupported_names_skipped:
        "Databases with names bekup cannot handle safely were skipped.",
      server_not_supported: "The server's version is older than every supported release.",
      no_allowlisted_client_for_server: "No allowlisted client image fits this server's version.",
      roles_without_passwords: "Some roles have no password set.",
    },
    reasons: {
      superuser: "Superuser",
      createrole: "May create roles (CREATEROLE)",
      replication: "Replication role",
      bypassrls: "Bypasses row-level security",
      memberOf: (value: string | undefined) => `Member of ${value ?? "a privileged role"}`,
      memberOfRoleWith: (value: string | undefined) =>
        `Member of a role with ${value ?? "privileges"}`,
      global: (value: string | undefined) => `Global privilege ${value ?? ""}`.trim(),
      proxy: "Has PROXY rights",
      systemSchemaWrite: "Can write to the system schema",
      rolesGranted: "Roles are granted (not inspected)",
      detectionFailed: "The check could not complete",
    },
  },
  nl: {
    tls: {
      PINNED: {
        label: "Vastgepinde TLS",
        short: "Vastgepind",
        description:
          "TLS gecontroleerd tegen een certificaat dat je hebt bevestigd (een zelfondertekend servercertificaat of je eigen private CA). De veilige standaard.",
      },
      UNVERIFIED: {
        label: "TLS zonder controle",
        short: "Niet gecontroleerd",
        description:
          "Versleuteld, maar iedereen op het netwerkpad kan zich voordoen als de server en de inloggegevens en data lezen.",
      },
      DISABLED: {
        label: "Geen TLS",
        short: "Uitgeschakeld",
        description:
          "Inloggegevens en elke byte van de back-up gaan onversleuteld over het netwerk. Alleen voor een database die je via een SSH-tunnel of op dezelfde host bereikt.",
      },
    },
    pin: {
      expires: (until: string) => `verloopt ${until}`,
      none: "Geen pin",
      missing: "Pin ontbreekt",
      missingDetail: "bevestig een certificaat om deze verbinding te gebruiken",
      valid: "Pin geldig",
      expires30: "Verloopt < 30 dagen",
      expires7: "Verloopt < 7 dagen",
      expired: "Pin verlopen",
    },
    privilege: {
      NOT_PRIVILEGED: {
        label: "Niet bevoorrecht",
        description:
          "Er is geen recht gevonden dat toegang tot de host of escalatie van rechten mogelijk maakt.",
      },
      PRIVILEGED: {
        label: "Bevoorrechte rol",
        description:
          "De rol kan de host bereiken of rechten escaleren. SQL in een herstelde dump draait met deze rechten, dus herstel naar deze verbinding vereist een beheerder en een uitdrukkelijke bevestiging.",
      },
      UNKNOWN: {
        label: "Rechten onbekend",
        description:
          "De rechten konden niet volledig worden vastgesteld en gelden als bevoorrecht: herstel vereist een beheerder en een uitdrukkelijke bevestiging.",
      },
    },
    warnings: {
      tls_staged_pin_in_use:
        "De server toonde het klaargezette reservecertificaat: bevestig het om het tot pin te maken.",
      tls_certificate_expires_within_30_days:
        "De certificaatketen van de server verloopt binnen 30 dagen.",
      tls_certificate_expires_within_7_days:
        "De certificaatketen van de server verloopt binnen 7 dagen.",
      non_innodb_tables:
        "Sommige tabellen gebruiken geen InnoDB: hun dump komt niet uit één consistente snapshot.",
      databases_with_unsupported_names_skipped:
        "Databases met namen die bekup niet veilig kan verwerken zijn overgeslagen.",
      server_not_supported: "De versie van de server is ouder dan elke ondersteunde release.",
      no_allowlisted_client_for_server:
        "Geen client-image uit de toegestane lijst past bij de versie van deze server.",
      roles_without_passwords: "Sommige rollen hebben geen wachtwoord.",
    },
    reasons: {
      superuser: "Superuser",
      createrole: "Mag rollen aanmaken (CREATEROLE)",
      replication: "Replicatierol",
      bypassrls: "Omzeilt beveiliging op rijniveau",
      memberOf: (value: string | undefined) => `Lid van ${value ?? "een bevoorrechte rol"}`,
      memberOfRoleWith: (value: string | undefined) => `Lid van een rol met ${value ?? "rechten"}`,
      global: (value: string | undefined) => `Globaal recht ${value ?? ""}`.trim(),
      proxy: "Heeft PROXY-rechten",
      systemSchemaWrite: "Kan naar het systeemschema schrijven",
      rolesGranted: "Er zijn rollen toegekend (niet onderzocht)",
      detectionFailed: "De controle kon niet worden afgerond",
    },
  },
});

export const TLS_MODE: Record<
  ConnectionTlsMode,
  { label: string; short: string; description: string; strength: number }
> = {
  PINNED: { ...L.tls.PINNED, strength: 2 },
  UNVERIFIED: { ...L.tls.UNVERIFIED, strength: 1 },
  DISABLED: { ...L.tls.DISABLED, strength: 0 },
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
  const detail = until ? L.pin.expires(until) : undefined;
  const withDetail = (m: { label: string; tone: Tone }) => (detail ? { ...m, detail } : m);
  switch (status) {
    case "NOT_REQUIRED":
      return { label: L.pin.none, tone: "neutral" };
    case "MISSING":
      return { label: L.pin.missing, tone: "danger", detail: L.pin.missingDetail };
    case "VALID":
      return withDetail({ label: L.pin.valid, tone: "success" });
    case "EXPIRES_WITHIN_30_DAYS":
      return withDetail({ label: L.pin.expires30, tone: "warning" });
    case "EXPIRES_WITHIN_7_DAYS":
      return withDetail({ label: L.pin.expires7, tone: "danger" });
    case "EXPIRED":
      return withDetail({ label: L.pin.expired, tone: "danger" });
  }
}

export const PRIVILEGE_META: Record<
  TargetPrivilegeStatus,
  { label: string; tone: Tone; description: string }
> = {
  NOT_PRIVILEGED: { ...L.privilege.NOT_PRIVILEGED, tone: "success" },
  PRIVILEGED: { ...L.privilege.PRIVILEGED, tone: "warning" },
  UNKNOWN: { ...L.privilege.UNKNOWN, tone: "warning" },
};

const WARNINGS: Record<string, string> = L.warnings;

/** A readable sentence for a stable warning code (the code itself when unknown). */
export function warningText(code: string): string {
  return WARNINGS[code] ?? code.replaceAll("_", " ");
}

/** A readable form of a privilege reason code (e.g. member_of:pg_execute_server_program). */
export function privilegeReason(code: string): string {
  const [kind, value] = code.split(/:(.*)/s, 2);
  switch (kind) {
    case "superuser":
      return L.reasons.superuser;
    case "createrole":
      return L.reasons.createrole;
    case "replication":
      return L.reasons.replication;
    case "bypassrls":
      return L.reasons.bypassrls;
    case "member_of":
      return L.reasons.memberOf(value);
    case "member_of_role_with":
      return L.reasons.memberOfRoleWith(value);
    case "global":
      return L.reasons.global(value);
    case "proxy":
      return L.reasons.proxy;
    case "system_schema_write":
      return L.reasons.systemSchemaWrite;
    case "roles_granted":
      return L.reasons.rolesGranted;
    case "detection_failed":
      return L.reasons.detectionFailed;
    default:
      return code;
  }
}
