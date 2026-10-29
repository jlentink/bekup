import {
  Fingerprint,
  KeyRound,
  LifeBuoy,
  LockKeyhole,
  Loader2,
  type LucideIcon,
} from "lucide-react";
import { useEffect, useId, useRef, useState, type SyntheticEvent } from "react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ApiCode, apiErrors, userMessage } from "@/lib/api-error";
import { messages } from "@/lib/messages";
import { cn } from "@/lib/utils";
import { WebAuthnError, webauthnSupported } from "@/lib/webauthn";

import { CodeInput } from "./code-input";
import { TOTP_DIGITS } from "./code-input-utils";

export type ChallengeMethod = "PASSKEY" | "TOTP" | "RECOVERY_CODE" | "PASSWORD";

const METHOD_ICON: Record<ChallengeMethod, LucideIcon> = {
  PASSKEY: Fingerprint,
  TOTP: KeyRound,
  RECOVERY_CODE: LifeBuoy,
  PASSWORD: LockKeyhole,
};

const M = messages.secondFactor;

export interface ChallengeFormProps {
  /** Offered methods in order of preference. */
  methods: readonly ChallengeMethod[];
  /** Runs the passkey ceremony and submits it; throws on failure. */
  onPasskey: () => Promise<void>;
  /** Submits a code or password; throws on failure. */
  onSecret: (method: Exclude<ChallengeMethod, "PASSKEY">, value: string) => Promise<void>;
  /** Label of the submit button (default "Verify"). */
  submitLabel?: string;
  /** Focus the first field when shown. */
  autoFocus?: boolean;
  /** Extra hint under the password field. */
  passwordHint?: string;
}

/** Turns a failure into a message for the field or for the whole form. */
function describeFailure(err: unknown): { field?: string; form?: string } {
  if (err instanceof WebAuthnError) {
    return { form: err.kind === "unsupported" ? M.passkeyUnsupported : M.passkeyCancelled };
  }
  const first = apiErrors(err)[0];
  if (
    first?.code === ApiCode.validation &&
    first.field !== "assertion" &&
    first.field !== "method"
  ) {
    return { field: userMessage(err) };
  }
  return { form: userMessage(err) };
}

/**
 * The second-factor form shared by the login's second step and the
 * step-up dialog: a method switch (native radios) and the field or button
 * of the chosen method. Errors land on the field (VALIDATION) or above the
 * form (lockout, expired ceremony, cancelled passkey).
 */
export function ChallengeForm({
  methods,
  onPasskey,
  onSecret,
  submitLabel = M.verify,
  autoFocus = true,
  passwordHint,
}: ChallengeFormProps) {
  const passkeyOK = webauthnSupported();
  const usable = methods.filter((m) => m !== "PASSKEY" || passkeyOK);
  const [method, setMethod] = useState<ChallengeMethod | undefined>(usable[0]);
  const [value, setValue] = useState("");
  const [fieldError, setFieldError] = useState<string>();
  const [formError, setFormError] = useState<string>();
  const [busy, setBusy] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const passkeyRef = useRef<HTMLButtonElement>(null);
  const name = useId();
  const fieldId = useId();
  const errorId = useId();
  const hintId = useId();
  const switched = useRef(false);

  // Move focus to the new method's control after switching (not on first render
  // unless autoFocus asked for it).
  useEffect(() => {
    if (!switched.current && !autoFocus) return;
    switched.current = true;
    (method === "PASSKEY" ? passkeyRef.current : inputRef.current)?.focus();
  }, [method, autoFocus]);

  if (!method) {
    return (
      <Alert variant="destructive">
        <AlertDescription>
          {methods.includes("PASSKEY") ? M.passkeyUnsupported : messages.stepUp.noMethods}
        </AlertDescription>
      </Alert>
    );
  }

  const fail = (err: unknown) => {
    const { field, form } = describeFailure(err);
    setFieldError(field);
    setFormError(form);
    if (field) inputRef.current?.focus();
  };

  const runPasskey = async () => {
    setBusy(true);
    setFieldError(undefined);
    setFormError(undefined);
    try {
      await onPasskey();
    } catch (err) {
      fail(err);
    } finally {
      setBusy(false);
    }
  };

  const submit = async (e: SyntheticEvent) => {
    e.preventDefault();
    if (method === "PASSKEY") return;
    const v = method === "RECOVERY_CODE" ? value.trim() : value;
    let local: string | undefined;
    if (method === "TOTP" && v.length !== TOTP_DIGITS) local = M.totpInvalidFormat;
    if (method === "RECOVERY_CODE" && v === "") local = M.recoveryRequired;
    if (method === "PASSWORD" && v === "") local = messages.stepUp.passwordRequired;
    if (local) {
      setFieldError(local);
      setFormError(undefined);
      inputRef.current?.focus();
      return;
    }
    setBusy(true);
    setFieldError(undefined);
    setFormError(undefined);
    try {
      await onSecret(method, v);
    } catch (err) {
      fail(err);
    } finally {
      setBusy(false);
    }
  };

  const describedBy = [fieldError ? errorId : undefined, hintId].filter(Boolean).join(" ");

  return (
    <div className="flex flex-col gap-5">
      {usable.length > 1 ? (
        <fieldset className="grid auto-cols-fr grid-flow-col gap-0.5 rounded-md border bg-surface-sunken p-0.5">
          <legend className="sr-only">Method</legend>
          {usable.map((m) => {
            const Icon = METHOD_ICON[m];
            return (
              <label
                key={m}
                className={cn(
                  "relative flex h-8 cursor-pointer items-center justify-center gap-1.5 rounded-[calc(var(--radius)-2px)] px-2 text-xs font-medium text-muted-foreground transition-colors",
                  "hover:text-foreground has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-ring",
                  method === m && "bg-surface text-foreground shadow-raised",
                )}
              >
                <input
                  type="radio"
                  name={name}
                  value={m}
                  checked={method === m}
                  disabled={busy}
                  onChange={() => {
                    switched.current = true;
                    setMethod(m);
                    setValue("");
                    setFieldError(undefined);
                    setFormError(undefined);
                  }}
                  className="sr-only"
                />
                <Icon aria-hidden="true" className="size-3.5 shrink-0" />
                <span className="truncate">{M.methods[m]}</span>
              </label>
            );
          })}
        </fieldset>
      ) : null}

      {formError ? (
        <Alert variant="destructive">
          <AlertDescription>{formError}</AlertDescription>
        </Alert>
      ) : null}

      {method === "PASSKEY" ? (
        <div className="flex flex-col gap-3">
          <p id={hintId} className="text-sm text-muted-foreground">
            {M.passkeyLead}
          </p>
          <Button
            ref={passkeyRef}
            type="button"
            className="w-full"
            disabled={busy}
            aria-describedby={hintId}
            onClick={() => void runPasskey()}
          >
            {busy ? (
              <Loader2 aria-hidden="true" className="animate-spin" />
            ) : (
              <Fingerprint aria-hidden="true" />
            )}
            {busy ? M.passkeyWorking : M.passkeyButton}
          </Button>
        </div>
      ) : (
        <form onSubmit={(e) => void submit(e)} noValidate className="flex flex-col gap-4">
          <div className="flex flex-col gap-2">
            <Label htmlFor={fieldId}>
              {method === "TOTP"
                ? M.totpLabel
                : method === "RECOVERY_CODE"
                  ? M.recoveryLabel
                  : messages.stepUp.passwordLabel}
            </Label>
            {method === "TOTP" ? (
              <CodeInput
                ref={inputRef}
                id={fieldId}
                value={value}
                onChange={setValue}
                aria-invalid={fieldError ? true : undefined}
                aria-describedby={describedBy}
                disabled={busy}
              />
            ) : (
              <Input
                ref={inputRef}
                id={fieldId}
                type={method === "PASSWORD" ? "password" : "text"}
                autoComplete={method === "PASSWORD" ? "current-password" : "off"}
                autoCapitalize="none"
                spellCheck={false}
                value={value}
                onChange={(e) => {
                  setValue(e.target.value);
                }}
                aria-invalid={fieldError ? true : undefined}
                aria-describedby={describedBy}
                disabled={busy}
                className={method === "RECOVERY_CODE" ? "font-mono tracking-wider" : undefined}
              />
            )}
            {fieldError ? (
              <p id={errorId} className="text-sm text-destructive">
                {fieldError}
              </p>
            ) : null}
            <p id={hintId} className="text-xs text-muted-foreground">
              {method === "TOTP"
                ? M.totpHint
                : method === "RECOVERY_CODE"
                  ? M.recoveryHint
                  : (passwordHint ?? "")}
            </p>
          </div>
          <Button type="submit" className="w-full" disabled={busy}>
            {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
            {busy ? M.verifying : submitLabel}
          </Button>
        </form>
      )}
    </div>
  );
}
