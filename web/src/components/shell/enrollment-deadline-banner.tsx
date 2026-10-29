import { ShieldAlert } from "lucide-react";
import { Link } from "react-router";

import { enrollmentState, type SessionUser } from "@/features/auth/session-model";
import { formatDateTime } from "@/lib/format";
import { messages } from "@/lib/messages";

/**
 * Shown during the 2FA enrollment grace period (D-112): the account's role
 * requires a second factor and the deadline lies ahead.
 */
export function EnrollmentDeadlineBanner({ user }: { user: SessionUser }) {
  if (enrollmentState(user) !== "grace" || !user.twoFactorEnrollmentDeadline) return null;
  return (
    <div
      role="status"
      data-slot="enrollment-deadline-banner"
      className="flex flex-wrap items-center gap-x-4 gap-y-1 border-b border-status-partial/40 bg-status-partial-soft px-4 py-2 text-status-partial-fg sm:px-6"
    >
      <ShieldAlert aria-hidden="true" className="size-4 shrink-0" />
      <p className="min-w-0 flex-1 text-[0.8125rem]">
        {messages.shell.enrollmentDeadline(formatDateTime(user.twoFactorEnrollmentDeadline))}
      </p>
      <Link
        to="/profile/security"
        className="text-[0.8125rem] font-semibold underline underline-offset-4 hover:no-underline"
      >
        {messages.shell.enrollmentDeadlineAction}
      </Link>
    </div>
  );
}
