import { useQuery } from "@apollo/client/react";
import { Fingerprint, Loader2, RotateCcw, ShieldPlus } from "lucide-react";
import { useId } from "react";
import { Link } from "react-router";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";

import { confirmationMatches } from "./model";
import { RestoreFactorStatusQuery } from "./operations";
import type { Preview } from "./preview-step";

export function ConfirmStep({
  preview,
  confirmation,
  onConfirmation,
  enrollmentNeeded,
  submitting,
  submitError,
  canSubmit,
  onSubmit,
}: {
  preview: Preview;
  confirmation: string;
  onConfirmation: (v: string) => void;
  enrollmentNeeded: boolean;
  submitting: boolean;
  submitError: string | undefined;
  canSubmit: boolean;
  onSubmit: () => void;
}) {
  const inputId = useId();
  const hintId = useId();
  const factors = useQuery(RestoreFactorStatusQuery, { fetchPolicy: "network-only" });
  const hasFactor =
    factors.data !== undefined &&
    (factors.data.myTwoFactorStatus.totpEnabled || factors.data.myPasskeys.length > 0);
  const noFactor = enrollmentNeeded || (factors.data !== undefined && !hasFactor);
  const globals = preview.source.kind === "GLOBALS";
  const text = preview.confirmText;
  const matches = confirmationMatches(text, confirmation);

  return (
    <div className="flex flex-col gap-8">
      {text ? (
        <div className="flex flex-col gap-3">
          <p id={hintId} className="max-w-prose text-sm">
            {globals ? (
              <>
                The globals restore changes roles on the whole server. Type the phrase{" "}
                <span className="font-mono font-semibold select-all">{text}</span> to confirm.
              </>
            ) : (
              <>
                <span className="font-mono font-semibold select-all">{text}</span> will be replaced
                as a whole. Type its name exactly to confirm.
              </>
            )}
          </p>
          <label htmlFor={inputId} className="sr-only">
            {globals ? "Confirmation phrase" : "Database name to confirm"}
          </label>
          <Input
            id={inputId}
            value={confirmation}
            autoComplete="off"
            spellCheck={false}
            aria-describedby={hintId}
            aria-invalid={confirmation !== "" && !matches}
            className={cn(
              "h-12 max-w-xl font-mono text-base",
              matches && "border-status-success focus-visible:border-status-success",
            )}
            onChange={(e) => {
              onConfirmation(e.target.value);
            }}
          />
        </div>
      ) : (
        <p className="text-sm text-muted-foreground">
          A new database is created; nothing existing is replaced.
        </p>
      )}

      <div className="flex flex-col gap-3 rounded-md border bg-surface-sunken/60 p-4">
        <div className="flex items-start gap-3">
          <Fingerprint aria-hidden="true" className="mt-0.5 size-5 shrink-0 text-primary" />
          <div className="flex flex-col gap-1">
            <span className="font-medium">Second-factor check</span>
            <p className="text-sm text-muted-foreground">
              Restores need a fresh check with a passkey (with user verification) or your
              authenticator app. Recovery codes and passwords do not count.
            </p>
          </div>
        </div>
        {factors.loading ? <Skeleton className="h-6 w-48" /> : null}
        {noFactor ? (
          <Alert variant="destructive">
            <ShieldPlus aria-hidden="true" />
            <AlertTitle>Set up a passkey or an authenticator app first</AlertTitle>
            <AlertDescription>
              <p>Your account has no second factor that can confirm a restore.</p>
              <Link
                to="/profile/security"
                className="font-medium text-primary underline underline-offset-4"
              >
                Open two-factor settings
              </Link>
            </AlertDescription>
          </Alert>
        ) : null}
      </div>

      {submitError ? (
        <Alert variant="destructive">
          <AlertTitle>The restore was not started</AlertTitle>
          <AlertDescription>{submitError}</AlertDescription>
        </Alert>
      ) : null}

      <div>
        <Button
          size="lg"
          variant={preview.mode === "OVERWRITE" || globals ? "destructive" : "default"}
          disabled={!canSubmit || !matches || noFactor || submitting || factors.loading}
          onClick={onSubmit}
        >
          {submitting ? (
            <Loader2 aria-hidden="true" className="animate-spin" />
          ) : (
            <RotateCcw aria-hidden="true" />
          )}
          Verify and start the restore
        </Button>
      </div>
    </div>
  );
}
