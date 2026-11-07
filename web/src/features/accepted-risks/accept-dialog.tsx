import { useApolloClient, useMutation } from "@apollo/client/react";
import { Loader2, ShieldAlert } from "lucide-react";
import { useId, useState, type SyntheticEvent } from "react";
import { toast } from "sonner";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { isStepUpCancelled, useStepUp } from "@/features/auth/use-step-up";
import type { RiskDuration } from "@/graphql/gen/graphql";
import { ApiCode, firstApiError, hasApiCode, userMessage } from "@/lib/api-error";
import { messages } from "@/lib/messages";
import { AcceptRiskMutation, SecurityRisksQuery } from "@/lib/operations";
import { cn } from "@/lib/utils";

import { T } from "./messages";
import { MAX_REASON, reasonProblem, type RiskRef } from "./model";
import { RiskAcceptancesQuery } from "./operations";

const DURATIONS: readonly RiskDuration[] = ["ONE_MONTH", "ONE_YEAR", "FOREVER"];

/**
 * Accepts a risk (D-228): the condition as the server showed it, a
 * duration and a reason. Submitting asks for a step-up; a changed condition
 * (CONFLICT) asks the caller to review it again. Mount it only while open.
 */
export function AcceptRiskDialog({
  risk,
  onOpenChange,
}: {
  risk: RiskRef;
  onOpenChange: (open: boolean) => void;
}) {
  const { withStepUp } = useStepUp();
  const client = useApolloClient();
  const [accept] = useMutation(AcceptRiskMutation, {
    refetchQueries: [SecurityRisksQuery, RiskAcceptancesQuery],
  });
  const [duration, setDuration] = useState<RiskDuration>("ONE_YEAR");
  const [reason, setReason] = useState("");
  const [error, setError] = useState<{ reason?: string; form?: string }>({});
  const [busy, setBusy] = useState(false);
  const ids = { reason: useId(), reasonHint: useId(), duration: useId() };

  const submit = (e: SyntheticEvent) => {
    e.preventDefault();
    const p = reasonProblem(reason);
    if (p) {
      setError({ reason: p === "missing" ? T.dialog.reasonMissing : T.dialog.reasonTooLong });
      return;
    }
    setError({});
    setBusy(true);
    const input = {
      kind: risk.kind,
      conditionId: risk.conditionId,
      connectionId: risk.connectionId ?? null,
      jobId: risk.jobId ?? null,
      duration,
      reason: reason.trim(),
    };
    withStepUp(() => accept({ variables: { input } }))
      .then(() => {
        toast.success(T.accepted);
        onOpenChange(false);
      })
      .catch((err: unknown) => {
        if (isStepUpCancelled(err)) return;
        if (hasApiCode(err, ApiCode.conflict)) {
          // The shown condition is stale: load the current one, so the
          // next attempt names it.
          setError({ form: T.dialog.changed });
          void client.refetchQueries({ include: [SecurityRisksQuery] });
          return;
        }
        setError(
          firstApiError(err)?.field === "reason"
            ? { reason: userMessage(err) }
            : { form: userMessage(err) },
        );
      })
      .finally(() => {
        setBusy(false);
      });
  };

  return (
    <Dialog
      open
      onOpenChange={(o) => {
        if (!busy) onOpenChange(o);
      }}
    >
      <DialogContent className="gap-5 sm:max-w-lg">
        <DialogHeader>
          <p className="eyebrow">{T.kinds[risk.kind]}</p>
          <DialogTitle className="font-display text-2xl font-normal">{T.dialog.title}</DialogTitle>
          <DialogDescription>{T.dialog.lead}</DialogDescription>
        </DialogHeader>
        <form noValidate onSubmit={submit} className="flex flex-col gap-5">
          <figure className="hatch-insecure rounded-md border border-status-insecure/50 px-4 py-3">
            <figcaption className="eyebrow mb-1 text-status-insecure-fg">
              {T.dialog.condition}
              {risk.subjectName ? ` · ${risk.subjectName}` : ` · ${T.instance}`}
            </figcaption>
            <p className="text-sm font-medium text-foreground">{risk.condition}</p>
          </figure>

          <fieldset className="flex flex-col gap-2" aria-describedby={ids.duration}>
            <legend className="mb-1 text-sm font-medium">{T.dialog.duration}</legend>
            <div className="grid grid-cols-3 gap-2">
              {DURATIONS.map((d) => (
                <label
                  key={d}
                  className={cn(
                    "flex cursor-pointer items-center justify-center gap-2 rounded-md border bg-surface px-3 py-2 text-sm has-[:focus-visible]:ring-[3px] has-[:focus-visible]:ring-ring/50",
                    duration === d ? "border-primary/50 bg-accent" : "hover:border-border-strong",
                    d === "FOREVER" && duration === d && "border-status-insecure/60",
                  )}
                >
                  <input
                    type="radio"
                    name="risk-duration"
                    className="size-4 accent-primary"
                    checked={duration === d}
                    onChange={() => {
                      setDuration(d);
                    }}
                  />
                  {T.durations[d]}
                </label>
              ))}
            </div>
            {duration === "FOREVER" ? (
              <p
                id={ids.duration}
                className="flex items-start gap-2 text-xs text-status-insecure-fg"
              >
                <ShieldAlert aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" />
                {T.dialog.foreverWarning}
              </p>
            ) : (
              <span id={ids.duration} hidden />
            )}
          </fieldset>

          <div className="grid gap-2">
            <Label htmlFor={ids.reason}>{T.dialog.reason}</Label>
            <textarea
              id={ids.reason}
              rows={3}
              maxLength={MAX_REASON + 20}
              className="w-full rounded-md border border-input bg-surface px-3 py-2 text-sm shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 aria-invalid:border-destructive dark:bg-surface-sunken"
              value={reason}
              aria-invalid={error.reason ? true : undefined}
              aria-describedby={ids.reasonHint}
              onChange={(e) => {
                setReason(e.target.value);
              }}
            />
            <p
              id={ids.reasonHint}
              className={cn("text-xs", error.reason ? "text-destructive" : "text-muted-foreground")}
            >
              {error.reason ?? T.dialog.reasonHint}
            </p>
          </div>

          {error.form ? (
            <Alert variant="destructive">
              <AlertDescription>{error.form}</AlertDescription>
            </Alert>
          ) : null}
          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" variant="outline" disabled={busy}>
                {messages.common.cancel}
              </Button>
            </DialogClose>
            <Button type="submit" disabled={busy}>
              {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
              {T.dialog.submit}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
