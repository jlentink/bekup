import { skipToken, useMutation, useQuery } from "@apollo/client/react";
import { CircleCheck, CircleX, Loader2, MailQuestion } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { Link, useLocation, useNavigate } from "react-router";

import { Button } from "@/components/ui/button";
import { AuthLayout } from "@/features/auth/auth-layout";
import { MeQuery } from "@/features/auth/operations";
import { ApiCode, firstApiError, userMessage } from "@/lib/api-error";
import { loginPathFor } from "@/lib/return-path";

import { notificationsMessages as M } from "./messages";
import { codeFromFragment } from "./model";
import { VerifyEmailMutation } from "./operations";
import { holdPending, resetPendingVerification, takePending } from "./pending-verification";

type Outcome = { kind: "done" } | { kind: "failed" } | { kind: "error"; message: string };

/**
 * /verify-email#code=… (D-178): the code sits in the URL fragment so it
 * never reaches a server or proxy log. The page moves it into memory,
 * replaces the history entry with a fragment-less URL before any request
 * is made, and calls verifyEmail (POST body) once the session is known.
 */
export function VerifyEmailPage() {
  const location = useLocation();
  const navigate = useNavigate();
  // The code: from the fragment on arrival, else from the sign-in detour.
  const [code] = useState(() => codeFromFragment(location.hash) ?? takePending());
  const [outcome, setOutcome] = useState<Outcome | null>(null);
  const [attempt, setAttempt] = useState(0);
  const started = useRef(-1);

  // Before paint and before any request: keep the code, drop the fragment.
  useLayoutEffect(() => {
    if (location.hash === "") return;
    if (code) holdPending(code);
    void navigate({ pathname: location.pathname, search: "", hash: "" }, { replace: true });
  }, [code, location.hash, location.pathname, navigate]);

  const cleared = location.hash === "";
  const me = useQuery(MeQuery, cleared ? { fetchPolicy: "cache-first" } : skipToken);
  const [verify] = useMutation(VerifyEmailMutation);

  const signInRequired = (() => {
    const c = firstApiError(me.error)?.code;
    return c === ApiCode.unauthenticated || c === ApiCode.secondFactorRequired;
  })();
  const signedIn = cleared && !me.error && Boolean(me.data?.me);

  useEffect(() => {
    if (!code || !signedIn || started.current === attempt) return;
    started.current = attempt;
    verify({ variables: { code } })
      .then(() => {
        resetPendingVerification();
        setOutcome({ kind: "done" });
      })
      .catch((err: unknown) => {
        if (firstApiError(err)?.code === ApiCode.validation) {
          resetPendingVerification();
          setOutcome({ kind: "failed" });
        } else {
          // Network trouble or a restricted session: the code stays for a retry.
          setOutcome({ kind: "error", message: userMessage(err) });
        }
      });
  }, [code, signedIn, verify, attempt]);

  const settings = (
    <Button asChild variant="outline">
      <Link to="/profile/notifications">{M.verify.toSettings}</Link>
    </Button>
  );

  let title: string = M.verify.working;
  let lead: string = M.verify.workingLead;
  let body = (
    <p className="flex items-center gap-2 text-sm text-muted-foreground" role="status">
      <Loader2 aria-hidden="true" className="size-4 animate-spin" /> {M.verify.working}
    </p>
  );

  if (signInRequired) {
    title = M.verify.signInTitle;
    lead = M.verify.signInLead;
    body = (
      <Button asChild>
        <Link to={loginPathFor("/verify-email")}>{M.verify.signIn}</Link>
      </Button>
    );
  } else if (!code) {
    title = M.verify.missingTitle;
    lead = M.verify.missingLead;
    body = (
      <div className="flex flex-col items-start gap-4">
        <MailQuestion aria-hidden="true" className="size-6 text-muted-foreground" />
        {settings}
      </div>
    );
  } else if (outcome?.kind === "done") {
    title = M.verify.doneTitle;
    lead = M.verify.doneLead;
    body = (
      <div className="flex flex-col items-start gap-4">
        <p role="status" className="flex items-center gap-2 text-sm text-status-success-fg">
          <CircleCheck aria-hidden="true" className="size-5" /> {M.verify.doneTitle}
        </p>
        <div className="flex flex-wrap gap-2">
          {settings}
          <Button asChild variant="ghost">
            <Link to="/">{M.verify.toDashboard}</Link>
          </Button>
        </div>
      </div>
    );
  } else if (outcome?.kind === "failed") {
    title = M.verify.failedTitle;
    lead = M.verify.failedLead;
    body = (
      <div className="flex flex-col items-start gap-4">
        <p role="alert" className="flex items-center gap-2 text-sm text-status-failed-fg">
          <CircleX aria-hidden="true" className="size-5" /> {M.verify.failedTitle}
        </p>
        {settings}
      </div>
    );
  } else if (outcome?.kind === "error") {
    body = (
      <div className="flex flex-col items-start gap-4">
        <p role="alert" className="text-sm text-destructive">
          {outcome.message}
        </p>
        <Button
          onClick={() => {
            setOutcome(null);
            setAttempt((a) => a + 1);
          }}
        >
          {M.verify.retry}
        </Button>
      </div>
    );
  }

  return (
    <AuthLayout eyebrow={M.verify.eyebrow} title={title} lead={lead}>
      {body}
    </AuthLayout>
  );
}
