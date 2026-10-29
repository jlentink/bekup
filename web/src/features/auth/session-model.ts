import type { MeQuery as MeQueryResult } from "@/graphql/gen/graphql";

export type SessionUser = NonNullable<MeQueryResult["me"]>;

/**
 * Where the account stands with the 2FA policy (D-112): "none" when no
 * factor is required or one exists, "grace" while the enrollment deadline
 * lies ahead (full access, a banner), "required" once it has passed (only
 * enrollment is allowed, REQUIRES_2FA_ENROLLMENT).
 */
export type EnrollmentState = "none" | "grace" | "required";

export function enrollmentState(
  user: Pick<SessionUser, "twoFactorRequired" | "twoFactorEnrollmentDeadline">,
  now: number = Date.now(),
): EnrollmentState {
  if (!user.twoFactorRequired || !user.twoFactorEnrollmentDeadline) return "none";
  const deadline = Date.parse(user.twoFactorEnrollmentDeadline);
  if (Number.isNaN(deadline)) return "required";
  return deadline <= now ? "required" : "grace";
}
