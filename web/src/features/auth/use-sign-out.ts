import { useApolloClient, useMutation } from "@apollo/client/react";
import { useCallback, useRef } from "react";
import { useNavigate } from "react-router";
import { toast } from "sonner";

import { ApiCode, firstApiError, type ApiErrorInfo } from "@/lib/api-error";
import { messages } from "@/lib/messages";

import { LogoutMutation } from "./operations";

/** Error codes worth retrying: the server may simply be busy or unreachable. */
const TRANSIENT = new Set<string>(["NETWORK", ApiCode.rateLimited, ApiCode.internal]);

/** Backoff between attempts (ms); three attempts in total. */
export const SIGN_OUT_BACKOFF_MS: readonly number[] = [500, 1500];

export type SignOutOutcome = { ok: true } | { ok: false; error: ApiErrorInfo };

/**
 * Ends the session on the server. Only a confirmed logout or an
 * UNAUTHENTICATED answer (the session already ended) count as signed out;
 * transient failures are retried with backoff, anything else is returned
 * so the caller can stay signed in and say so (a session the user thinks
 * has ended must not silently live on).
 */
export async function endSession(
  logout: () => Promise<unknown>,
  sleep: (ms: number) => Promise<void> = (ms) => new Promise((r) => setTimeout(r, ms)),
  backoff: readonly number[] = SIGN_OUT_BACKOFF_MS,
): Promise<SignOutOutcome> {
  for (let attempt = 0; ; attempt++) {
    let error: ApiErrorInfo | undefined;
    try {
      const res = (await logout()) as { error?: unknown } | undefined;
      if (res?.error) error = firstApiError(res.error);
    } catch (err) {
      error = firstApiError(err) ?? { code: "NETWORK", message: messages.errors.network };
    }
    if (!error || error.code === ApiCode.unauthenticated) return { ok: true };
    const delay = backoff[attempt];
    if (!TRANSIENT.has(error.code) || delay === undefined) return { ok: false, error };
    await sleep(delay);
  }
}

/**
 * Signs out: ends the session on the server, then drops every cached
 * object and goes to login. When the server cannot confirm the logout the
 * user stays on the page with an error toast.
 */
export function useSignOut(): () => Promise<void> {
  const client = useApolloClient();
  const navigate = useNavigate();
  const [logout] = useMutation(LogoutMutation);
  const busy = useRef(false);
  return useCallback(async () => {
    if (busy.current) return;
    busy.current = true;
    try {
      const outcome = await endSession(() => logout());
      if (!outcome.ok) {
        toast.error(messages.common.signOutFailed, { description: outcome.error.message });
        return;
      }
      await client.clearStore();
      await navigate("/login", { replace: true });
    } finally {
      busy.current = false;
    }
  }, [client, logout, navigate]);
}
