import { createContext, useCallback, useContext, useMemo } from "react";
import { useNavigate } from "react-router";
import { toast } from "sonner";

import { ApiCode, hasApiCode, userMessage } from "@/lib/api-error";
import { messages } from "@/lib/messages";

/** Thrown by withStepUp when the user closes the dialog. Callers ignore it. */
export class StepUpCancelledError extends Error {
  constructor() {
    super("step-up cancelled");
    this.name = "StepUpCancelledError";
  }
}

export function isStepUpCancelled(err: unknown): err is StepUpCancelledError {
  return err instanceof StepUpCancelledError;
}

export interface StepUpOptions {
  /**
   * The operation needs a second factor check (AAL2, restores): only a
   * passkey or a TOTP code are offered (D-057, D-116).
   */
  aal2?: boolean;
}

export type RequestStepUp = (opts?: StepUpOptions) => Promise<void>;

export const StepUpContext = createContext<RequestStepUp | null>(null);

/** How many times one operation may ask for a step-up before giving up. */
const MAX_STEP_UPS = 2;

export interface UseStepUp {
  /**
   * Runs op; when it fails with STEP_UP_REQUIRED, asks the user to confirm
   * their identity and runs it again. REQUIRES_2FA_ENROLLMENT sends the
   * user to the two-factor settings. Rejects with StepUpCancelledError
   * when the dialog is closed, otherwise with op's error.
   */
  withStepUp: <T>(op: () => Promise<T>, opts?: StepUpOptions) => Promise<T>;
  /** Opens the dialog directly (e.g. before a multi-step flow). */
  requestStepUp: RequestStepUp;
}

export function useStepUp(): UseStepUp {
  const requestStepUp = useContext(StepUpContext);
  if (!requestStepUp) throw new Error("useStepUp must be used inside <StepUpProvider>");
  const navigate = useNavigate();

  const withStepUp = useCallback(
    async <T>(op: () => Promise<T>, opts: StepUpOptions = {}): Promise<T> => {
      for (let attempt = 0; ; attempt++) {
        try {
          return await op();
        } catch (err) {
          if (hasApiCode(err, ApiCode.stepUpRequired) && attempt < MAX_STEP_UPS) {
            await requestStepUp(opts);
            continue;
          }
          if (hasApiCode(err, ApiCode.requires2FAEnrollment)) {
            toast.warning(messages.stepUp.enrollmentNeeded, {
              id: "enrollment-needed",
              action: {
                label: messages.shell.enrollmentDeadlineAction,
                onClick: () => {
                  void navigate("/profile/security");
                },
              },
            });
          }
          throw err;
        }
      }
    },
    [requestStepUp, navigate],
  );

  return useMemo(() => ({ withStepUp, requestStepUp }), [withStepUp, requestStepUp]);
}

/**
 * Toasts a failed account action, except a closed step-up dialog (the
 * user chose to stop). Returns the error message shown, if any.
 */
export function reportActionError(err: unknown): string | undefined {
  if (isStepUpCancelled(err)) return undefined;
  const message = userMessage(err);
  toast.error(message);
  return message;
}
