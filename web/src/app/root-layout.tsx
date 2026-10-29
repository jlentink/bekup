import { useApolloClient } from "@apollo/client/react";
import { useEffect, useRef, useState } from "react";
import { Outlet, useLocation, useNavigate } from "react-router";
import { toast } from "sonner";

import { MeQuery } from "@/features/auth/operations";
import { StepUpProvider } from "@/features/auth/step-up";
import { ApiCode, firstApiError } from "@/lib/api-error";
import { type AuthEvent, onAuthEvent } from "@/lib/auth-events";
import { messages } from "@/lib/messages";
import { loginPathFor, withReturnParam } from "@/lib/return-path";

import { SessionEpochContext } from "./session-epoch";

function isAuthPage(pathname: string): boolean {
  return pathname === "/login" || pathname.startsWith("/login/");
}

/**
 * Root of every route: hosts the step-up dialog and turns auth events from
 * the Apollo links into navigation (D-105 error codes):
 *
 * - unauthenticated: drop the cache, go to login with the current page as
 *   the (validated) return path;
 * - second-factor-required: go to the verify page;
 * - restricted: refetch `me`, the session gate shows the forced flow;
 * - session-check: ask the server first (a live stream ended), then either
 *   reopen streams or treat it as unauthenticated.
 */
export function RootLayout() {
  const client = useApolloClient();
  const navigate = useNavigate();
  const location = useLocation();
  const [epoch, setEpoch] = useState(0);
  // The latest location for the event handler without re-subscribing.
  const here = useRef(location);
  useEffect(() => {
    here.current = location;
  }, [location]);

  useEffect(() => {
    const signedOut = async (notice: string) => {
      const { pathname, search, hash } = here.current;
      if (pathname === "/login") return;
      await client.clearStore();
      toast.info(notice, { id: "session-ended" });
      await navigate(loginPathFor(pathname + search + hash), { replace: true });
    };

    const handle = async (event: AuthEvent) => {
      const { pathname, search, hash } = here.current;
      switch (event) {
        case "unauthenticated":
          await signedOut(
            pathname.startsWith("/login/")
              ? messages.secondFactor.timedOut
              : messages.errors.sessionEnded,
          );
          return;
        case "second-factor-required":
          if (isAuthPage(pathname)) return;
          await navigate(
            withReturnParam(
              "/login/verify",
              `?next=${encodeURIComponent(pathname + search + hash)}`,
            ),
            { replace: true },
          );
          return;
        case "restricted":
          await client.refetchQueries({ include: [MeQuery] });
          return;
        case "session-check":
          try {
            const r = await client.query({ query: MeQuery, fetchPolicy: "network-only" });
            if (r.data?.me) setEpoch((e) => e + 1);
            else await signedOut(messages.errors.sessionEnded);
          } catch (err) {
            const code = firstApiError(err)?.code;
            if (code === ApiCode.unauthenticated) await signedOut(messages.errors.sessionEnded);
            else if (code === ApiCode.secondFactorRequired) await handle("second-factor-required");
            // Network trouble: keep the page; the next request decides.
          }
          return;
      }
    };

    return onAuthEvent((event) => {
      void handle(event);
    });
  }, [client, navigate]);

  return (
    <SessionEpochContext.Provider value={epoch}>
      <StepUpProvider>
        <Outlet />
      </StepUpProvider>
    </SessionEpochContext.Provider>
  );
}
