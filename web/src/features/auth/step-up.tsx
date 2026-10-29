import { useMutation } from "@apollo/client/react";
import { ShieldCheck } from "lucide-react";
import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import type {
  BeginStepUpMutation as BeginStepUpMutationResult,
  StepUpMethod,
} from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { messages } from "@/lib/messages";
import { getAssertion } from "@/lib/webauthn";

import { ChallengeForm, type ChallengeMethod } from "./challenge-form";
import { BeginStepUpMutation, CompleteStepUpMutation } from "./operations";
import {
  StepUpCancelledError,
  StepUpContext,
  type RequestStepUp,
  type StepUpOptions,
} from "./use-step-up";

interface PendingRequest {
  opts: StepUpOptions;
  resolve: () => void;
  reject: (err: unknown) => void;
}

/**
 * Hosts the step-up dialog. requestStepUp() opens it and settles when the
 * user confirmed (resolve) or closed it (StepUpCancelledError). Concurrent
 * requests share one dialog.
 */
export function StepUpProvider({ children }: { children: ReactNode }) {
  const [request, setRequest] = useState<PendingRequest | null>(null);
  const inflight = useRef<Promise<void> | null>(null);

  const requestStepUp = useCallback<RequestStepUp>((opts = {}) => {
    if (inflight.current) return inflight.current;
    const p = new Promise<void>((resolve, reject) => {
      setRequest({ opts, resolve, reject });
    }).finally(() => {
      inflight.current = null;
      setRequest(null);
    });
    inflight.current = p;
    return p;
  }, []);

  return (
    <StepUpContext.Provider value={requestStepUp}>
      {children}
      {request ? (
        <StepUpDialog
          aal2={request.opts.aal2 === true}
          onDone={request.resolve}
          onCancel={() => {
            request.reject(new StepUpCancelledError());
          }}
        />
      ) : null}
    </StepUpContext.Provider>
  );
}

const AAL2_METHODS: ReadonlySet<StepUpMethod> = new Set(["PASSKEY", "TOTP"]);

function StepUpDialog({
  aal2,
  onDone,
  onCancel,
}: {
  aal2: boolean;
  onDone: () => void;
  onCancel: () => void;
}) {
  const [begin] = useMutation(BeginStepUpMutation);
  const [complete] = useMutation(CompleteStepUpMutation);
  const [challenge, setChallenge] = useState<BeginStepUpMutationResult["beginStepUp"]>();
  const [loadError, setLoadError] = useState<string>();
  const started = useRef(false);

  useEffect(() => {
    // Once per dialog (StrictMode runs effects twice): a second begin
    // would replace the session's passkey challenge.
    if (started.current) return;
    started.current = true;
    begin()
      .then((r) => {
        if (r.data) setChallenge(r.data.beginStepUp);
      })
      .catch((err: unknown) => {
        setLoadError(userMessage(err));
      });
  }, [begin]);

  const methods: ChallengeMethod[] = (challenge?.methods ?? []).filter(
    (m) => !aal2 || AAL2_METHODS.has(m),
  );
  const hasPassword = methods.length === 1 && methods[0] === "PASSWORD";

  const onPasskey = async () => {
    const options = challenge?.passkeyOptions;
    if (!options) throw new Error("no passkey challenge");
    try {
      const assertion = await getAssertion(options);
      await complete({ variables: { method: "PASSKEY", assertion } });
    } catch (err) {
      // A passkey challenge is single use: fetch a fresh one for the next try.
      const fresh = await begin().catch(() => undefined);
      if (fresh?.data) setChallenge(fresh.data.beginStepUp);
      throw err;
    }
    onDone();
  };

  const onSecret = async (method: Exclude<ChallengeMethod, "PASSKEY">, value: string) => {
    await complete({
      variables: method === "PASSWORD" ? { method, password: value } : { method, code: value },
    });
    onDone();
  };

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onCancel();
      }}
    >
      <DialogContent className="gap-6 sm:max-w-md">
        <DialogHeader className="gap-3">
          <span
            aria-hidden="true"
            className="inline-flex size-9 items-center justify-center rounded-md border border-primary/30 bg-accent text-primary"
          >
            <ShieldCheck className="size-4.5" />
          </span>
          <DialogTitle className="font-display text-2xl font-normal">
            {messages.stepUp.title}
          </DialogTitle>
          <DialogDescription>
            {aal2 ? messages.stepUp.leadAal2 : messages.stepUp.lead}
          </DialogDescription>
        </DialogHeader>
        {loadError ? (
          <Alert variant="destructive">
            <AlertDescription>{loadError}</AlertDescription>
          </Alert>
        ) : !challenge ? (
          <div className="flex flex-col gap-3" aria-busy="true">
            <Skeleton className="h-9 w-full" />
            <Skeleton className="h-12 w-full" />
            <Skeleton className="h-9 w-full" />
          </div>
        ) : methods.length === 0 ? (
          <Alert variant="destructive">
            <AlertDescription>
              {aal2 ? messages.stepUp.enrollmentNeeded : messages.stepUp.noMethods}
            </AlertDescription>
          </Alert>
        ) : (
          <ChallengeForm
            methods={methods}
            onPasskey={onPasskey}
            onSecret={onSecret}
            submitLabel={messages.stepUp.confirm}
            {...(hasPassword ? { passwordHint: messages.stepUp.passwordHint } : {})}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}
