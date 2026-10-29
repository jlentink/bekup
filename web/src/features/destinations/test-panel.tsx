import { useMutation } from "@apollo/client/react";
import { Check, Loader2, Minus, Radar, X } from "lucide-react";
import { useState } from "react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { userMessage } from "@/lib/api-error";
import { formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

import { stepLabel } from "./labels";
import { TestDestinationMutation } from "./operations";
import { StoredTestPanel, type StoredTest } from "@/features/connections/last-test";

import type { DestinationTestResult } from "./results";

/** The normalized testDestination result as a step ledger. */
export function DestinationTestResultView({
  result,
  at,
}: {
  result: DestinationTestResult;
  at: number;
}) {
  return (
    <div
      role="status"
      className={cn(
        "flex flex-col gap-3 rounded-md border-l-2 bg-surface-sunken/60 px-4 py-3.5",
        result.ok ? "border-l-status-success" : "border-l-status-failed",
      )}
    >
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <p className="font-medium">{result.ok ? "Destination works" : "Destination test failed"}</p>
        <span className="font-mono text-2xs text-muted-foreground">
          tested {formatRelative(new Date(at).toISOString())}
        </span>
      </div>
      <ol aria-label="Test steps" className="flex flex-col">
        {result.steps.map((step, i) => {
          const Icon = step.ok ? Check : X;
          return (
            <li
              key={`${step.name}-${i}`}
              className="grid grid-cols-[1.25rem_7rem_minmax(0,1fr)] items-baseline gap-3 border-b border-rule py-2 last:border-b-0"
            >
              <Icon
                aria-hidden="true"
                className={cn(
                  "size-3.5 self-center",
                  step.ok ? "text-status-success" : "text-status-failed",
                )}
              />
              <span className="text-sm">
                {stepLabel(step.name)}
                <span className="sr-only">: {step.ok ? "passed" : "failed"}</span>
              </span>
              <span className="flex flex-wrap items-baseline gap-2 text-sm text-muted-foreground">
                {step.ok ? "OK" : step.message}
                {!step.ok ? <span className="font-mono text-2xs">{step.code}</span> : null}
              </span>
            </li>
          );
        })}
        {result.steps.length === 0 ? (
          <li className="flex items-center gap-2 text-sm text-muted-foreground">
            <Minus aria-hidden="true" className="size-3.5" /> No steps ran.
          </li>
        ) : null}
      </ol>
      {result.probeKey ? (
        <p className="text-xs text-muted-foreground">
          Probe object <code className="font-mono break-all">{result.probeKey}</code>
          {result.probeRemains
            ? " was left in place (no-delete, Object Lock or a failed delete)."
            : " was removed."}
        </p>
      ) : null}
      {result.note ? <p className="text-sm">{result.note}</p> : null}
    </div>
  );
}

/** Runs testDestination (rate limited, its own request) and shows the last result. */
export function DestinationTest({
  id,
  lastTest,
}: {
  id: string;
  /** The stored last test (D-187), shown until a test runs on this page. */
  lastTest?: StoredTest | null;
}) {
  const [test] = useMutation(TestDestinationMutation);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  // The full step ledger of a test run on this page; the stored outcome
  // is refetched with the destination afterwards (a separate request).
  const [last, setLast] = useState<{ value: DestinationTestResult; at: number }>();

  const run = async () => {
    setBusy(true);
    setError(undefined);
    try {
      const r = await test({ variables: { id }, refetchQueries: ["DestinationDetail"] });
      if (r.data) setLast({ value: r.data.testDestination, at: Date.now() });
    } catch (err) {
      setError(userMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex flex-col gap-4">
      <Button
        type="button"
        size="sm"
        className="self-start"
        disabled={busy}
        onClick={() => void run()}
      >
        {busy ? (
          <Loader2 aria-hidden="true" className="animate-spin" />
        ) : (
          <Radar aria-hidden="true" />
        )}
        Test destination
      </Button>
      {error ? (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : null}
      {last ? (
        <DestinationTestResultView result={last.value} at={last.at} />
      ) : (
        <StoredTestPanel
          test={lastTest}
          emptyText="Never tested. The test writes, reads back and deletes a small probe object."
        />
      )}
    </div>
  );
}
