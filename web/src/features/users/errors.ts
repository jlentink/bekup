import { toast } from "sonner";

import { reportActionError } from "@/features/auth/use-step-up";
import { ApiCode, hasApiCode } from "@/lib/api-error";

import { usersMessages as M } from "./messages";

/**
 * Reports a failed account change. CONFLICT from demoting, disabling or
 * deleting an account is the last-admin invariant (D-110): explain it
 * instead of the generic conflict text.
 */
export function reportUserAdminError(err: unknown, lastAdminPossible = true): void {
  if (lastAdminPossible && hasApiCode(err, ApiCode.conflict)) {
    toast.error(M.lastAdmin, { id: "last-admin" });
    return;
  }
  reportActionError(err);
}
