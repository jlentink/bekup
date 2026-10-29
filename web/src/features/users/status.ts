import type { AdminUserFieldsFragment, Role } from "@/graphql/gen/graphql";

/** Display-only account state. The server enforces every one of these. */
export type AccountFlag =
  "deleted" | "disabled" | "locked" | "temporary" | "temporaryExpired" | "active";

type FlagInput = Pick<
  AdminUserFieldsFragment,
  "deletedAt" | "disabled" | "lockedUntil" | "passwordChangeRequired" | "temporaryPasswordExpiresAt"
>;

function future(iso: string | null | undefined, now: number): boolean {
  if (!iso) return false;
  const t = Date.parse(iso);
  return !Number.isNaN(t) && t > now;
}

/**
 * The flags of an account, most important first. "active" only when
 * nothing else applies; a deleted account shows only "deleted".
 */
export function accountFlags(u: FlagInput, now: number = Date.now()): AccountFlag[] {
  if (u.deletedAt) return ["deleted"];
  const flags: AccountFlag[] = [];
  if (u.disabled) flags.push("disabled");
  if (future(u.lockedUntil, now)) flags.push("locked");
  if (u.passwordChangeRequired) {
    flags.push(
      u.temporaryPasswordExpiresAt && !future(u.temporaryPasswordExpiresAt, now)
        ? "temporaryExpired"
        : "temporary",
    );
  }
  return flags.length > 0 ? flags : ["active"];
}

/** True while a login lockout is in effect. */
export function isLocked(u: Pick<FlagInput, "lockedUntil">, now: number = Date.now()): boolean {
  return future(u.lockedUntil, now);
}

/**
 * Where the account stands with second factors and the 2FA policy (D-112,
 * D-184). `twoFactorEnrolled` (TOTP or a passkey; recovery codes alone do
 * not count) decides "enrolled" / "none"; without a factor a required
 * account is in its grace period until the deadline, then overdue.
 */
export type TwoFactorState = "enrolled" | "grace" | "overdue" | "none";

export function twoFactorState(
  u: Pick<
    AdminUserFieldsFragment,
    "twoFactorRequired" | "twoFactorEnrollmentDeadline" | "twoFactorEnrolled"
  >,
  now: number = Date.now(),
): TwoFactorState {
  if (u.twoFactorEnrolled) return "enrolled";
  if (!u.twoFactorRequired) return "none";
  return future(u.twoFactorEnrollmentDeadline, now) ? "grace" : "overdue";
}

/** Why an account cannot be chosen as a backup owner; null when it can (D-067). */
export type OwnershipBlock = "role" | "disabled" | "deleted";

export function ownershipBlock(u: {
  role: Role;
  disabled: boolean;
  deletedAt?: string | null;
}): OwnershipBlock | null {
  if (u.deletedAt) return "deleted";
  if (u.role !== "USER") return "role";
  if (u.disabled) return "disabled";
  return null;
}

export const ROLES: readonly Role[] = ["ADMIN", "MAINTAINER", "USER"];
