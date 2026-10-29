import { useQuery } from "@apollo/client/react";
import { type ReactNode } from "react";
import { Navigate, useLocation } from "react-router";

import { ShellSkeleton } from "@/components/shell/shell-skeleton";
import { FullPageError } from "@/components/full-page-error";
import { ApiCode, firstApiError } from "@/lib/api-error";
import { loginPathFor, RETURN_PARAM, safeReturnPath } from "@/lib/return-path";

import { MeQuery } from "./operations";
import { enrollmentState, type SessionUser } from "./session-model";
import { SessionContext } from "./use-session-user";

export function SessionProvider({ user, children }: { user: SessionUser; children: ReactNode }) {
  return <SessionContext.Provider value={user}>{children}</SessionContext.Provider>;
}

export interface RequireSessionProps {
  /** The app (the shell) for a fully signed-in session. */
  children: ReactNode;
  /** Rendered instead while the temporary password must be changed. */
  passwordChange: ReactNode;
  /** Rendered instead once a second factor must be enrolled. */
  enrollment: ReactNode;
}

/**
 * The session gate in front of the app: loads `me`, sends a missing or
 * ended session to the login page (with a validated return path), a login
 * waiting for its second factor to the verify page, and restricted
 * sessions to the forced flows. The server enforces all of this anyway
 * (authz.checkRestrictions); the gate only shapes the UI.
 */
export function RequireSession({ children, passwordChange, enrollment }: RequireSessionProps) {
  const location = useLocation();
  const { data, error, loading, refetch } = useQuery(MeQuery, { fetchPolicy: "cache-first" });
  const here = location.pathname + location.search + location.hash;

  if (error) {
    const code = firstApiError(error)?.code;
    if (code === ApiCode.unauthenticated) return <Navigate to={loginPathFor(here)} replace />;
    if (code === ApiCode.secondFactorRequired) {
      const next = safeReturnPath(here);
      const target =
        next && next !== "/"
          ? `/login/verify?${RETURN_PARAM}=${encodeURIComponent(next)}`
          : "/login/verify";
      return <Navigate to={target} replace />;
    }
    return (
      <FullPageError
        error={error}
        onRetry={() => {
          void refetch().catch(() => undefined);
        }}
      />
    );
  }
  if (!data) return loading ? <ShellSkeleton /> : <Navigate to={loginPathFor(here)} replace />;
  const user = data.me;
  if (!user) return <Navigate to={loginPathFor(here)} replace />;

  let content = children;
  if (user.passwordChangeRequired) content = passwordChange;
  else if (enrollmentState(user) === "required") content = enrollment;
  return <SessionProvider user={user}>{content}</SessionProvider>;
}
