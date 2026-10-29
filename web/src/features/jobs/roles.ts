import { useSessionUser } from "@/features/auth/use-session-user";

/**
 * What the signed-in role may see in the job and run pages. This only
 * shapes the UI (D-053); the server's authz matrix decides.
 */
export interface RoleFlags {
  admin: boolean;
  /** Admins and maintainers manage every job. */
  staff: boolean;
  /** Role USER: sees only the jobs they own (D-067). */
  owner: boolean;
  /** Downloads: admins and backup owners, never maintainers (D-012, D-150). */
  canDownload: boolean;
}

export function roleFlags(role: string): RoleFlags {
  const admin = role === "ADMIN";
  const staff = admin || role === "MAINTAINER";
  const owner = role === "USER";
  return { admin, staff, owner, canDownload: admin || owner };
}

export function useRoleFlags(): RoleFlags {
  return roleFlags(useSessionUser().role);
}
