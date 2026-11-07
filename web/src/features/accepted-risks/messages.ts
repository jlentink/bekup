import type {
  RiskAcceptanceStatus,
  RiskDuration,
  RiskEndReason,
  RiskKind,
} from "@/graphql/gen/graphql";
import { messages } from "@/lib/messages";

/** Strings of the accepted risks feature (D-228). */
export const T = {
  page: {
    eyebrow: "Security",
    title: "Accepted risks",
    lead: "Weakened settings that an administrator or a backup owner accepted. They stay marked, but they are no longer open issues on the dashboard, in reminders or in the summary. Every acceptance is audited; a changed condition is flagged again.",
    open: (n: number) =>
      n === 1
        ? "1 risk is open and can be accepted"
        : `${String(n)} risks are open and can be accepted`,
    openHeading: "Open risks",
    openLead: "These weakened settings are flagged as open issues now.",
    ledgerHeading: "Ledger",
    filters: { ACTIVE: "Active", ENDED: "Ended", ALL: "All" } as Record<string, string>,
    filterLabel: "Show",
    empty: "No accepted risks",
    emptyActive: "Nothing is accepted right now: every weakened setting is flagged.",
    emptyEnded: "No acceptance has ended yet.",
    emptyOpen: "No open risk can be accepted right now.",
  },
  columns: {
    risk: "Risk",
    subject: "On",
    reason: "Reason",
    by: "Accepted by",
    until: "Until",
    status: "Status",
  },
  instance: "This instance",
  deleted: "deleted",
  noEnd: "No end date",
  byYou: "You",
  unknownPerson: "Not shown",
  accept: "Accept risk",
  acceptedUntil: messages.risks.acceptedUntil,
  acceptedForever: messages.risks.acceptedForever,
  acceptAgain: "Renew",
  revoke: "Revoke",
  revokeTitle: "Revoke this acceptance?",
  revokeLead: (what: string) =>
    `${what} is flagged as an open issue again at once: on the dashboard, in reminders and in the summary. The revocation is audited.`,
  revoked: "Acceptance revoked; the risk is flagged again.",
  accepted: "Risk accepted.",
  dialog: {
    title: "Accept this risk",
    lead: "bekup stops flagging this weakened setting as an open issue. The badge stays, the decision is audited and the admins are alerted. If the condition changes, it is flagged again.",
    condition: "Condition",
    duration: "Accept for",
    reason: "Reason",
    reasonHint: "Why can't this be fixed? Shown to everyone who sees the risk (1–500 characters).",
    reasonMissing: "Say why the risk is accepted.",
    reasonTooLong: "At most 500 characters.",
    foreverWarning:
      "Without an end date the risk is never flagged again until someone revokes the acceptance or the condition changes. The admins get an error-level alert.",
    submit: "Accept risk",
    changed: "The condition changed since you opened this. Review the risk again.",
  },
  durations: {
    ONE_MONTH: "1 month",
    ONE_YEAR: "1 year",
    FOREVER: "Forever",
  } satisfies Record<RiskDuration, string>,
  kinds: {
    INSECURE_TRANSPORT: "Plain HTTP",
    INSECURE_DB_TLS: "App database TLS",
    INSECURE_DB_ROLES: "Single database account",
    UNPINNED_IMAGE: "Unpinned image",
    CONNECTION_TLS: "Connection TLS",
    PRIVILEGED_ROLE: "Privileged role",
    SMTP_UNENCRYPTED: "Unencrypted e-mail",
    ENCRYPTION_OPT_OUT: "Unencrypted backups",
  } satisfies Record<RiskKind, string>,
  statuses: {
    ACTIVE: "Active",
    EXPIRED: "Expired",
    REVOKED: "Revoked",
    ENDED: "Ended",
  } satisfies Record<RiskAcceptanceStatus, string>,
  endReasons: {
    REVOKED: "Revoked",
    SUPERSEDED: "Replaced by a new acceptance",
    EXPIRED: "Expired",
    CONDITION_CHANGED: "The condition changed",
    RESOLVED: "The risk was fixed",
    SUBJECT_DELETED: "Deleted",
    ACCEPTOR_LOST_RIGHTS: "The acceptor may no longer decide",
  } satisfies Record<RiskEndReason, string>,
};
