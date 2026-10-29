/**
 * Pure logic of the restore wizard (task 6.7): steps, what each step needs
 * before the next one, the request inputs, warning severities and the
 * privileged-target gate. The server checks all of it again (D-167, D-168);
 * this only shapes the UI.
 */
import type {
  RestoreArtifactInput,
  RestoreGlobalsInput,
  RestoreMode,
  RestorePreviewInput,
  RestoreStatus,
  Role,
  RunItemKind,
  TargetPrivilegeStatus,
} from "@/graphql/gen/graphql";
import { extractIdentity } from "@/lib/age-identity";

export type StepId = "source" | "target" | "options" | "preview" | "confirm" | "progress";

export const STEP_LABELS: Record<StepId, string> = {
  source: "Backup",
  target: "Target",
  options: "Options",
  preview: "Preview",
  confirm: "Confirm",
  progress: "Progress",
};

/** Globals restores have no database options (roles and tablespaces only). */
export function stepsFor(kind: RunItemKind | undefined): StepId[] {
  return kind === "GLOBALS"
    ? ["source", "target", "preview", "confirm", "progress"]
    : ["source", "target", "options", "preview", "confirm", "progress"];
}

export interface WizardState {
  artifactId: string | undefined;
  kind: RunItemKind | undefined;
  engine: string | undefined;
  needsIdentity: boolean;
  targetId: string | undefined;
  /** Whether the chosen target allows a new database / restricts existing ones. */
  targetReady: boolean;
  newDatabaseAllowed: boolean;
  mode: RestoreMode;
  existingDatabase: string;
  newDatabase: string;
  keepOwners: boolean;
  acknowledgePrivileged: boolean;
  confirmation: string;
}

export function initialState(artifactId?: string): WizardState {
  return {
    artifactId,
    kind: undefined,
    engine: undefined,
    needsIdentity: false,
    targetId: undefined,
    targetReady: false,
    newDatabaseAllowed: false,
    mode: "NEW_DB",
    existingDatabase: "",
    newDatabase: "",
    keepOwners: false,
    acknowledgePrivileged: false,
    confirmation: "",
  };
}

/** The database the restore writes (empty for globals). */
export function targetDatabaseOf(s: WizardState): string {
  if (s.kind === "GLOBALS") return "";
  return s.mode === "OVERWRITE" ? s.existingDatabase : s.newDatabase.trim();
}

/** Longest database name either engine accepts (PostgreSQL: 63 bytes, MySQL: 64 characters). */
const MAX_DB_NAME_BYTES = 63;

/** A problem with a new database name, or undefined. The server validates it again. */
export function newDatabaseProblem(name: string): string | undefined {
  const n = name.trim();
  if (n === "") return "Enter a name for the new database.";
  if (new TextEncoder().encode(n).length > MAX_DB_NAME_BYTES)
    return "Use at most 63 bytes for the database name.";
  // eslint-disable-next-line no-control-regex -- control characters are exactly what is refused
  if (/[\u0000-\u001f\u007f]/.test(n)) return "The name contains control characters.";
  if (/[/\\.`"']/.test(n)) return "Avoid quotes, slashes, backslashes and dots in the name.";
  if (/__bekup_/i.test(n) || /^bekup_test_/i.test(n)) return "That name is reserved by bekup.";
  return undefined;
}

/** Whether the user may leave step to the next one. */
export function canAdvance(step: StepId, s: WizardState, identity: string): boolean {
  switch (step) {
    case "source":
      return s.artifactId !== undefined && s.kind !== undefined;
    case "target":
      if (!s.targetId || !s.targetReady) return false;
      if (s.kind === "GLOBALS") return true;
      if (s.mode === "OVERWRITE") return s.existingDatabase !== "";
      return s.newDatabaseAllowed && newDatabaseProblem(s.newDatabase) === undefined;
    case "options":
      return !s.needsIdentity || extractIdentity(identity) !== null;
    default:
      return false;
  }
}

export type PrivilegeGate =
  /** Nothing to acknowledge. */
  | "clear"
  /** An admin must tick the acknowledgement first. */
  | "needs-ack"
  /** An admin ticked it. */
  | "acknowledged"
  /** Only admins may restore into this target (D-068). */
  | "blocked";

export function privilegeGate(
  privileges: { acknowledgementRequired: boolean } | undefined,
  role: Role,
  acknowledged: boolean,
): PrivilegeGate {
  if (!privileges?.acknowledgementRequired) return "clear";
  if (role !== "ADMIN") return "blocked";
  return acknowledged ? "acknowledged" : "needs-ack";
}

/** Whether the preview step may be left (the gate is passed). */
export function previewPasses(gate: PrivilegeGate): boolean {
  return gate === "clear" || gate === "acknowledged";
}

/**
 * Whether the typed confirmation matches what the server expects: the
 * database name for OVERWRITE, the phrase for globals, nothing otherwise.
 */
export function confirmationMatches(
  confirmText: string | null | undefined,
  typed: string,
): boolean {
  if (!confirmText) return true;
  return typed === confirmText;
}

export function previewInput(s: WizardState): RestorePreviewInput {
  if (!s.artifactId || !s.targetId) throw new Error("incomplete restore request");
  if (s.kind === "GLOBALS") return { artifactId: s.artifactId, targetConnectionId: s.targetId };
  return {
    artifactId: s.artifactId,
    targetConnectionId: s.targetId,
    mode: s.mode,
    targetDatabase: targetDatabaseOf(s),
    ...(s.engine === "POSTGRES" ? { keepOwners: s.keepOwners } : {}),
  };
}

/**
 * The restoreArtifact input. The identity is added only here, for the one
 * request that needs it; the caller drops it afterwards.
 */
export function artifactInput(
  s: WizardState,
  identity: string,
  acknowledgementRequired: boolean,
): RestoreArtifactInput {
  if (!s.artifactId || !s.targetId || s.kind !== "DATABASE")
    throw new Error("incomplete restore request");
  const db = targetDatabaseOf(s);
  const key = s.needsIdentity ? (extractIdentity(identity) ?? undefined) : undefined;
  return {
    artifactId: s.artifactId,
    targetConnectionId: s.targetId,
    mode: s.mode,
    targetDatabase: db,
    ...(s.mode === "OVERWRITE" ? { confirmTargetName: s.confirmation } : {}),
    ...(s.engine === "POSTGRES" ? { keepOwners: s.keepOwners } : {}),
    ...(key ? { ageIdentity: key } : {}),
    ...(acknowledgementRequired ? { acknowledgePrivilegedTarget: s.acknowledgePrivileged } : {}),
  };
}

export function globalsInput(
  s: WizardState,
  acknowledgementRequired: boolean,
): RestoreGlobalsInput {
  if (!s.artifactId || !s.targetId || s.kind !== "GLOBALS")
    throw new Error("incomplete restore request");
  return {
    artifactId: s.artifactId,
    targetConnectionId: s.targetId,
    confirmPhrase: s.confirmation,
    ...(acknowledgementRequired ? { acknowledgePrivilegedTarget: s.acknowledgePrivileged } : {}),
  };
}

export type WarningSeverity = "destructive" | "caution" | "info";

/** Codes of internal/restore/api.go: data loss or a server-wide change is red. */
const DESTRUCTIVE: ReadonlySet<string> = new Set([
  "overwrite_in_place",
  "overwrite_swap",
  "globals_change_roles",
  "privileged_target",
]);
const INFO: ReadonlySet<string> = new Set([
  "restore_hardening",
  "temporary_target",
  "identity_required",
]);

export function warningSeverity(code: string): WarningSeverity {
  if (DESTRUCTIVE.has(code)) return "destructive";
  if (INFO.has(code)) return "info";
  return "caution";
}

const SEVERITY_ORDER: Record<WarningSeverity, number> = { destructive: 0, caution: 1, info: 2 };

export function sortWarnings<T extends { code: string }>(warnings: readonly T[]): T[] {
  return [...warnings].sort(
    (a, b) => SEVERITY_ORDER[warningSeverity(a.code)] - SEVERITY_ORDER[warningSeverity(b.code)],
  );
}

export const PRIVILEGE_LABELS: Record<TargetPrivilegeStatus, string> = {
  NOT_PRIVILEGED: "Not privileged",
  PRIVILEGED: "Privileged",
  UNKNOWN: "Unknown (treated as privileged)",
};

/** Plain-language names of the D-068 reason codes; unknown codes are shown as they are. */
export function privilegeReasonLabel(code: string): string {
  const fixed: Record<string, string> = {
    superuser: "Superuser",
    createrole: "Can create roles (CREATEROLE)",
    replication: "Replication role",
    bypassrls: "Bypasses row-level security",
    proxy: "Proxy privilege",
    system_schema_write: "Can write the mysql system schema",
    roles_granted: "Has granted roles (could not be resolved completely)",
    detection_failed: "The check could not complete",
  };
  if (fixed[code]) return fixed[code];
  if (code.startsWith("member_of_role_with:"))
    return `Member of a role with ${code.slice("member_of_role_with:".length)}`;
  if (code.startsWith("member_of:")) return `Member of ${code.slice("member_of:".length)}`;
  if (code.startsWith("global:")) return `Global privilege ${code.slice("global:".length)}`;
  return code;
}

export const ACTIVE_STATUSES: ReadonlySet<RestoreStatus> = new Set([
  "QUEUED",
  "STAGING",
  "VERIFYING",
  "RESTORING",
]);

export function isActive(status: RestoreStatus): boolean {
  return ACTIVE_STATUSES.has(status);
}

/** The progress phases in order (the ledger strip of the progress view). */
export const PHASES: readonly RestoreStatus[] = ["QUEUED", "STAGING", "VERIFYING", "RESTORING"];

export const STATUS_LABELS: Record<RestoreStatus, string> = {
  QUEUED: "Queued",
  STAGING: "Staging",
  VERIFYING: "Verifying",
  RESTORING: "Restoring",
  SUCCEEDED: "Succeeded",
  FAILED: "Failed",
  CANCELED: "Canceled",
};

const NEUTRALIZED_PREFIX = "switched to SECURITY INVOKER: ";
const NEUTRALIZED_MORE = /^\.\.\. and (\d+) more$/;

/**
 * The SECURITY DEFINER routines the restore switched to SECURITY INVOKER
 * (D-171), read from the restore log the worker writes
 * (internal/restore/postgres.go).
 */
export function neutralizedFunctions(lines: readonly { line: string }[]): {
  functions: string[];
  more: number;
} {
  const functions: string[] = [];
  let more = 0;
  for (const { line } of lines) {
    if (line.startsWith(NEUTRALIZED_PREFIX)) functions.push(line.slice(NEUTRALIZED_PREFIX.length));
    const m = NEUTRALIZED_MORE.exec(line);
    if (m?.[1]) more = Number(m[1]);
  }
  return { functions, more };
}

export const ENGINE_LABELS: Record<string, string> = {
  POSTGRES: "PostgreSQL",
  MYSQL: "MySQL",
  MARIADB: "MariaDB",
};
