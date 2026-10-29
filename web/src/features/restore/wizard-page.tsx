import { skipToken, useApolloClient, useQuery } from "@apollo/client/react";
import { Check, ChevronLeft, ChevronRight, History } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router";

import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { useSessionUser } from "@/features/auth/use-session-user";
import { isStepUpCancelled, useStepUp } from "@/features/auth/use-step-up";
import { getFragmentData } from "@/graphql/gen";
import { ApiCode, hasApiCode, userMessage } from "@/lib/api-error";
import { cn } from "@/lib/utils";

import { ConfirmStep } from "./confirm-step";
import {
  artifactInput,
  canAdvance,
  confirmationMatches,
  globalsInput,
  initialState,
  previewInput,
  previewPasses,
  privilegeGate,
  STEP_LABELS,
  stepsFor,
  targetDatabaseOf,
  type StepId,
  type WizardState,
} from "./model";
import {
  RestoreArtifactMutation,
  RestoreFieldsFragment,
  RestoreGlobalsMutation,
  RestorePreviewQuery,
  RestoreTargetsQuery,
} from "./operations";
import { OptionsStep } from "./options-step";
import { PreviewStep } from "./preview-step";
import { RestoreProgress } from "./progress";
import { SourceStep, type PickedArtifact } from "./source-step";
import { TargetStep, type TargetChoice } from "./target-step";

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

const STEP_LEADS: Record<StepId, string> = {
  source: "Only verified backups covered by a signed manifest can be restored.",
  target: "Choose where the backup goes. Connections must use the backup's engine.",
  options: "How the restored objects are owned, and the key for recipient-encrypted backups.",
  preview: "What bekup will do, checked by the server. Read every warning.",
  confirm: "Confirm the target, then prove it is you with a second factor.",
  progress: "Live status of the restore.",
};

/**
 * The restore wizard (task 6.7): backup → target → options → preview →
 * confirm (typed name or phrase + AAL2 step-up) → progress. Globals
 * backups (admins) skip the options. The age identity lives only in this
 * component's state and is cleared once the request was sent.
 */
export function RestoreWizardPage() {
  const user = useSessionUser();
  const client = useApolloClient();
  const { withStepUp, requestStepUp } = useStepUp();
  const [params, setParams] = useSearchParams();
  const urlArtifact = params.get("artifactId");
  const [state, setState] = useState<WizardState>(() =>
    initialState(urlArtifact && UUID_RE.test(urlArtifact) ? urlArtifact : undefined),
  );
  const [picked, setPicked] = useState<PickedArtifact>();
  const [identity, setIdentity] = useState("");
  const [step, setStep] = useState<StepId>("source");
  const [restoreId, setRestoreId] = useState<string>();
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string>();
  const [enrollmentNeeded, setEnrollmentNeeded] = useState(false);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const firstRender = useRef(true);

  const targetsQ = useQuery(
    RestoreTargetsQuery,
    state.artifactId
      ? { variables: { artifactId: state.artifactId }, fetchPolicy: "network-only" }
      : skipToken,
  );
  const source = state.artifactId ? targetsQ.data?.restoreTargets.source : undefined;
  const targets = state.artifactId ? (targetsQ.data?.restoreTargets.targets ?? []) : [];
  const target = targets.find((t) => t.id === state.targetId);

  // Facts that come from the server, merged into the wizard state.
  const full: WizardState = {
    ...state,
    kind: source?.kind,
    engine: source?.engine,
    needsIdentity: source?.needsIdentity ?? false,
    targetReady: target?.ready ?? false,
    newDatabaseAllowed: target?.newDatabaseAllowed ?? false,
  };
  const steps = stepsFor(full.kind);
  const index = steps.indexOf(step);

  const previewActive = (step === "preview" || step === "confirm") && full.targetId !== undefined;
  const previewQ = useQuery(
    RestorePreviewQuery,
    previewActive
      ? { variables: { input: previewInput(full) }, fetchPolicy: "network-only" }
      : skipToken,
  );
  const preview = previewActive ? previewQ.data?.restorePreview : undefined;
  const gate = privilegeGate(preview?.targetPrivileges, user.role, state.acknowledgePrivileged);

  useEffect(() => {
    // Move focus to the new step's heading (not on the first render).
    if (firstRender.current) {
      firstRender.current = false;
      return;
    }
    headingRef.current?.focus();
  }, [step]);

  const update = (next: Partial<WizardState>) => {
    setState((s) => ({ ...s, ...next }));
  };
  /** A changed request invalidates the acknowledgement and the typed confirmation. */
  const updateRequest = (next: Partial<WizardState>) => {
    update({ ...next, acknowledgePrivileged: false, confirmation: "" });
    setSubmitError(undefined);
  };

  const pick = (a: PickedArtifact) => {
    setPicked(a);
    setIdentity("");
    setState(initialState(a.id));
    setParams({ artifactId: a.id }, { replace: true });
  };
  const clearSource = () => {
    setPicked(undefined);
    setIdentity("");
    setState(initialState());
    setParams({}, { replace: true });
  };

  const canNext =
    step === "preview"
      ? preview !== undefined && previewPasses(gate)
      : canAdvance(step, full, identity);
  const goNext = () => {
    const next = steps[index + 1];
    if (next && next !== "progress") setStep(next);
  };
  const goBack = () => {
    const prev = steps[index - 1];
    if (prev) setStep(prev);
  };

  const submit = async () => {
    if (!preview) return;
    setSubmitError(undefined);
    setEnrollmentNeeded(false);
    setSubmitting(true);
    const request = { sent: false };
    const ackRequired = preview.targetPrivileges.acknowledgementRequired;
    try {
      // A restore always needs a fresh passkey or TOTP check (AAL2, D-116):
      // ask for it first so the request goes out only once it is done.
      await requestStepUp({ aal2: true });
      const id = await withStepUp(
        async () => {
          request.sent = true;
          if (full.kind === "GLOBALS") {
            const r = await client.mutate({
              mutation: RestoreGlobalsMutation,
              variables: { input: globalsInput(full, ackRequired) },
              fetchPolicy: "no-cache",
            });
            return getFragmentData(RestoreFieldsFragment, r.data?.restoreGlobals)?.id;
          }
          const r = await client.mutate({
            mutation: RestoreArtifactMutation,
            variables: { input: artifactInput(full, identity, ackRequired) },
            fetchPolicy: "no-cache",
          });
          return getFragmentData(RestoreFieldsFragment, r.data?.restoreArtifact)?.id;
        },
        { aal2: true },
      );
      if (!id) throw new Error("no restore returned");
      setRestoreId(id);
      setStep("progress");
    } catch (err) {
      if (isStepUpCancelled(err)) return;
      if (hasApiCode(err, ApiCode.requires2FAEnrollment)) setEnrollmentNeeded(true);
      else setSubmitError(userMessage(err));
    } finally {
      // The identity never outlives the request it was sent with.
      if (request.sent) setIdentity("");
      setSubmitting(false);
    }
  };

  const targetChoice: TargetChoice = {
    targetId: state.targetId,
    mode: state.mode,
    existingDatabase: state.existingDatabase,
    newDatabase: state.newDatabase,
  };

  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        eyebrow="Restore"
        title={full.kind === "GLOBALS" ? "Restore cluster globals" : "Restore a backup"}
        description="Every restore is checked by the server, confirmed with a second factor and audited."
        actions={
          <Button variant="outline" size="sm" asChild>
            <Link to="/restores">
              <History aria-hidden="true" />
              Restore history
            </Link>
          </Button>
        }
      />
      <div className="grid gap-8 lg:grid-cols-[13rem_minmax(0,1fr)]">
        <StepRail steps={steps} current={step} />
        <section aria-labelledby="restore-step-heading" className="flex min-w-0 flex-col gap-6">
          <header className="flex flex-col gap-1 border-b border-rule pb-4">
            <p className="eyebrow">
              Step {String(index + 1).padStart(2, "0")} of {String(steps.length).padStart(2, "0")}
            </p>
            <h2
              id="restore-step-heading"
              ref={headingRef}
              tabIndex={-1}
              className="font-display text-3xl leading-tight outline-none"
            >
              {STEP_LABELS[step]}
            </h2>
            <p className="text-sm text-muted-foreground">{STEP_LEADS[step]}</p>
          </header>

          {step === "source" ? (
            <SourceStep
              role={user.role}
              artifactId={state.artifactId}
              picked={picked}
              source={source}
              sourceLoading={targetsQ.loading}
              sourceError={targetsQ.error}
              onPick={pick}
              onClear={clearSource}
            />
          ) : null}
          {step === "target" && state.artifactId && full.kind && full.engine ? (
            <TargetStep
              role={user.role}
              artifactId={state.artifactId}
              kind={full.kind}
              engine={full.engine}
              targets={targets}
              loading={targetsQ.loading}
              error={targetsQ.error}
              choice={targetChoice}
              onChange={updateRequest}
              onTemporaryReady={(id) => {
                void targetsQ.refetch().then(() => {
                  updateRequest({ targetId: id, existingDatabase: "", mode: "NEW_DB" });
                });
              }}
            />
          ) : null}
          {step === "options" && full.engine ? (
            <OptionsStep
              engine={full.engine}
              mode={state.mode}
              targetDatabase={targetDatabaseOf(full)}
              targetName={target?.name ?? ""}
              keepOwners={state.keepOwners}
              onKeepOwners={(v) => {
                updateRequest({ keepOwners: v });
              }}
              needsIdentity={full.needsIdentity}
              identity={identity}
              onIdentity={setIdentity}
            />
          ) : null}
          {step === "preview" ? (
            <PreviewStep
              preview={preview}
              loading={previewQ.loading}
              error={previewQ.error}
              gate={gate}
              acknowledged={state.acknowledgePrivileged}
              onAcknowledge={(v) => {
                update({ acknowledgePrivileged: v });
              }}
            />
          ) : null}
          {step === "confirm" && preview ? (
            <ConfirmStep
              preview={preview}
              confirmation={state.confirmation}
              onConfirmation={(v) => {
                update({ confirmation: v });
              }}
              enrollmentNeeded={enrollmentNeeded}
              submitting={submitting}
              submitError={submitError}
              canSubmit={
                previewPasses(gate) && confirmationMatches(preview.confirmText, state.confirmation)
              }
              onSubmit={() => {
                void submit();
              }}
            />
          ) : null}
          {step === "progress" && restoreId ? <RestoreProgress restoreId={restoreId} /> : null}

          {step !== "progress" ? (
            <footer className="flex items-center gap-3 border-t border-rule pt-5">
              {index > 0 ? (
                <Button variant="ghost" onClick={goBack} disabled={submitting}>
                  <ChevronLeft aria-hidden="true" />
                  Back
                </Button>
              ) : null}
              {step !== "confirm" ? (
                <Button className="ml-auto" onClick={goNext} disabled={!canNext}>
                  Continue
                  <ChevronRight aria-hidden="true" />
                </Button>
              ) : null}
            </footer>
          ) : (
            <footer className="flex gap-3 border-t border-rule pt-5">
              <Button variant="outline" asChild>
                <Link to={`/restores/${restoreId ?? ""}`}>Open the restore record</Link>
              </Button>
              <Button variant="ghost" asChild>
                <Link to="/restores">Restore history</Link>
              </Button>
            </footer>
          )}
        </section>
      </div>
    </div>
  );
}

/** The ledger of steps: folio numbers, done marks, the current one ruled in ink. */
function StepRail({ steps, current }: { steps: StepId[]; current: StepId }) {
  const at = steps.indexOf(current);
  return (
    <nav aria-label="Restore steps">
      <ol className="flex gap-1 overflow-x-auto lg:flex-col lg:gap-0 lg:border-l lg:border-rule">
        {steps.map((s, i) => {
          const done = i < at;
          const now = i === at;
          return (
            <li
              key={s}
              aria-current={now ? "step" : undefined}
              className={cn(
                "flex shrink-0 items-center gap-3 rounded-sm px-3 py-2 text-sm lg:-ml-px lg:rounded-none lg:border-l-2 lg:border-transparent lg:py-2.5",
                now && "bg-accent font-medium text-foreground lg:border-primary lg:bg-transparent",
                !now && !done && "text-muted-foreground",
              )}
            >
              <span
                aria-hidden="true"
                className={cn(
                  "inline-flex size-6 items-center justify-center rounded-full border font-mono text-2xs",
                  done && "border-primary bg-primary text-primary-foreground",
                  now && "border-primary text-primary",
                )}
              >
                {done ? <Check className="size-3" /> : String(i + 1).padStart(2, "0")}
              </span>
              {STEP_LABELS[s]}
              <span className="sr-only">{done ? " (done)" : ""}</span>
            </li>
          );
        })}
      </ol>
    </nav>
  );
}
