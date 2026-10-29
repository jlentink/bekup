import { useApolloClient } from "@apollo/client/react";
import { LogOut } from "lucide-react";
import { useCallback } from "react";

import { Button } from "@/components/ui/button";
import { ChangePasswordForm } from "@/features/account/change-password-form";
import { TwoFactorPanel } from "@/features/account/two-factor-panel";
import { messages } from "@/lib/messages";

import { AuthLayout } from "./auth-layout";
import { MeQuery } from "./operations";
import { useSessionUser } from "./use-session-user";
import { useSignOut } from "./use-sign-out";

function SignOutButton() {
  const signOut = useSignOut();
  return (
    <Button variant="ghost" size="sm" onClick={() => void signOut()}>
      <LogOut aria-hidden="true" /> {messages.common.signOut}
    </Button>
  );
}

/** Refetches `me`; the session gate then shows the app or the next flow. */
function useRefreshSession(): () => void {
  const client = useApolloClient();
  return useCallback(() => {
    void client.refetchQueries({ include: [MeQuery] });
  }, [client]);
}

/**
 * A session signed in with a temporary password (D-110) may only change it
 * (PASSWORD_CHANGE_REQUIRED for everything else).
 */
export function ForcedPasswordChange() {
  const user = useSessionUser();
  const refresh = useRefreshSession();
  return (
    <AuthLayout
      eyebrow={messages.forced.passwordEyebrow}
      title={messages.forced.passwordTitle}
      lead={messages.forced.passwordLead}
      actions={<SignOutButton />}
    >
      <ChangePasswordForm
        account={[user.email, user.displayName]}
        currentLabel={messages.forced.temporaryPassword}
        onChanged={refresh}
      />
    </AuthLayout>
  );
}

/**
 * The 2FA policy requires a factor and the grace period is over (D-112):
 * only enrolling a passkey or an authenticator app is allowed.
 */
export function ForcedEnrollment() {
  const user = useSessionUser();
  const refresh = useRefreshSession();
  return (
    <AuthLayout
      eyebrow={messages.forced.enrollEyebrow}
      title={messages.forced.enrollTitle}
      lead={messages.forced.enrollLead}
      actions={<SignOutButton />}
      wide
    >
      <TwoFactorPanel account={user.email} onFactorAdded={refresh} showRecovery={false} />
    </AuthLayout>
  );
}
