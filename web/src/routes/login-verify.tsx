import { useApolloClient, useMutation } from "@apollo/client/react";
import { useState } from "react";
import { useLocation, useNavigate } from "react-router";

import { Button } from "@/components/ui/button";
import { AuthLayout } from "@/features/auth/auth-layout";
import { ChallengeForm, type ChallengeMethod } from "@/features/auth/challenge-form";
import {
  BeginPasskeyLoginMutation,
  CompleteSecondFactorMutation,
  LogoutMutation,
} from "@/features/auth/operations";
import { clearFactors, loadFactors } from "@/features/auth/pending-login";
import type { SecondFactorKind } from "@/graphql/gen/graphql";
import { messages } from "@/lib/messages";
import { RETURN_PARAM, safeReturnPath } from "@/lib/return-path";
import { getAssertion } from "@/lib/webauthn";

const M = messages.secondFactor;

/**
 * Sign-in, step 2: the second factor of a login waiting for it (5 minutes,
 * D-112). A passkey, a TOTP code or a recovery code completes it; the
 * session is then rotated to a full one by the server.
 */
export function LoginVerifyPage() {
  const navigate = useNavigate();
  const { search } = useLocation();
  const client = useApolloClient();
  const [factors] = useState<SecondFactorKind[]>(loadFactors);
  const [beginPasskey] = useMutation(BeginPasskeyLoginMutation);
  const [complete] = useMutation(CompleteSecondFactorMutation);
  const [logout] = useMutation(LogoutMutation);

  const finish = async () => {
    clearFactors();
    await client.clearStore();
    const next = safeReturnPath(new URLSearchParams(search).get(RETURN_PARAM)) ?? "/";
    await navigate(next, { replace: true });
  };

  const onPasskey = async () => {
    const { data } = await beginPasskey();
    if (!data) throw new Error("no passkey options");
    const assertion = await getAssertion(data.beginPasskeyLogin);
    await complete({ variables: { method: "PASSKEY", assertion } });
    await finish();
  };

  const onSecret = async (method: Exclude<ChallengeMethod, "PASSKEY">, code: string) => {
    if (method === "PASSWORD") throw new Error("password is not a second factor");
    await complete({ variables: { method, code } });
    await finish();
  };

  const switchAccount = async () => {
    await logout().catch(() => undefined);
    clearFactors();
    await client.clearStore();
    await navigate("/login", { replace: true });
  };

  return (
    <AuthLayout
      eyebrow={M.eyebrow}
      title={M.title}
      lead={M.lead}
      step={1}
      footer={messages.login.noRecoveryByMail}
    >
      <ChallengeForm methods={factors} onPasskey={onPasskey} onSecret={onSecret} />
      <Button
        variant="link"
        className="self-start px-0 text-muted-foreground"
        onClick={() => void switchAccount()}
      >
        {M.useOtherAccount}
      </Button>
    </AuthLayout>
  );
}
