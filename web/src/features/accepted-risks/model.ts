import type { RiskKind } from "@/graphql/gen/graphql";

/** The most characters of a reason (the database CHECK, D-228). */
export const MAX_REASON = 500;

/** A risk as the dialog needs it (from Query.securityRisks). */
export interface RiskRef {
  kind: RiskKind;
  conditionId: string;
  condition: string;
  connectionId?: string | null | undefined;
  jobId?: string | null | undefined;
  subjectName?: string | null | undefined;
}

/** The page a risk's subject lives on, or null for instance-wide kinds. */
export function subjectPath(r: {
  kind: RiskKind;
  connectionId?: string | null | undefined;
  jobId?: string | null | undefined;
}): string | null {
  if (r.connectionId) return `/connections/${r.connectionId}`;
  if (r.jobId) return `/jobs/${r.jobId}`;
  switch (r.kind) {
    case "SMTP_UNENCRYPTED":
      return "/admin/settings";
    case "INSECURE_TRANSPORT":
    case "INSECURE_DB_TLS":
    case "INSECURE_DB_ROLES":
    case "UNPINNED_IMAGE":
      return "/admin/settings/configuration";
    default:
      return null;
  }
}

/** The local validation of a reason (the server checks the same). */
export function reasonProblem(raw: string): "missing" | "long" | null {
  const s = raw.trim();
  if (s === "") return "missing";
  // Code points, as the server counts (utf8.RuneCountInString).
  if (Array.from(s).length > MAX_REASON) return "long";
  return null;
}

/** The covering acceptance of a kind and subject, if any. */
export function acceptanceFor<
  R extends {
    kind: RiskKind;
    connectionId?: string | null | undefined;
    jobId?: string | null | undefined;
    acceptance?: { expiresAt?: string | null | undefined } | null | undefined;
  },
>(risks: readonly R[] | undefined, kind: RiskKind, subjectId?: string): R | undefined {
  return risks?.find(
    (r) =>
      r.kind === kind &&
      (subjectId === undefined || r.connectionId === subjectId || r.jobId === subjectId),
  );
}
