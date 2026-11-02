import { useEffect, useId, useRef, useState, type SyntheticEvent } from "react";
import { ArrowLeft, Check, Copy, Eye, KeyRound } from "lucide-react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";

import { answer, answerSecret, back, reveal, SetupApiError, type Step } from "./api";
import { T } from "./messages";

/**
 * What the ledger records for an answered step (never a secret). Notes are
 * recorded without a value (shown null) and not listed: Back pops them too.
 */
export interface Entry {
  title: string;
  shown: string | null;
}

interface StepProps {
  step: Step;
  onAnswered: (entry: Entry) => void;
  onBack: () => void;
  canGoBack: boolean;
}

/** StepView renders one wizard screen by its kind. */
export function StepView(props: StepProps) {
  const { step } = props;
  return (
    <article aria-labelledby={`step-${step.id}`} className="setup-rise">
      <h2
        id={`step-${step.id}`}
        className="font-display text-display leading-tight tracking-tight text-balance"
      >
        {step.title}
      </h2>
      {step.body && step.kind !== "sensitive" && (
        <p className="mt-4 max-w-prose font-mono text-[0.8125rem] leading-relaxed whitespace-pre-wrap text-muted-foreground">
          {step.body}
        </p>
      )}
      {step.error && (
        <Alert variant="destructive" className="mt-5" role="alert">
          <AlertDescription>{step.error}</AlertDescription>
        </Alert>
      )}
      <div className="mt-8">
        {step.kind === "note" && <NoteStep {...props} />}
        {step.kind === "select" && <SelectStep {...props} />}
        {step.kind === "input" && <InputStep {...props} />}
        {step.kind === "confirm" && <ConfirmStep {...props} />}
        {step.kind === "secret" && <SecretStep {...props} />}
        {step.kind === "sensitive" && <SensitiveStep {...props} />}
      </div>
    </article>
  );
}

/** useSend wraps an API call with a pending flag and an error message. */
function useSend() {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const send = async (fn: () => Promise<void>) => {
    setPending(true);
    setError(null);
    try {
      await fn();
    } catch (err) {
      setError(err instanceof SetupApiError ? err.message : String(err));
      setPending(false);
      return false;
    }
    setPending(false);
    return true;
  };
  return { pending, error, send };
}

function Actions({
  pending,
  canGoBack,
  onBack,
  step,
  label = T.continue,
  error,
}: {
  pending: boolean;
  canGoBack: boolean;
  onBack: () => void;
  step: Step;
  label?: string;
  error: string | null;
}) {
  const { send, pending: backPending } = useSend();
  return (
    <div className="mt-8 flex flex-col gap-3">
      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
      <div className="flex items-center gap-3">
        {canGoBack && (
          <Button
            type="button"
            variant="ghost"
            disabled={pending || backPending}
            onClick={() =>
              void send(() => back(step.id)).then((ok) => {
                if (ok) onBack();
              })
            }
          >
            <ArrowLeft aria-hidden /> {T.back}
          </Button>
        )}
        <Button type="submit" disabled={pending || backPending} className="ml-auto min-w-36">
          {pending ? T.sending : label}
        </Button>
      </div>
    </div>
  );
}

function NoteStep({ step, onAnswered, onBack, canGoBack }: StepProps) {
  const { pending, error, send } = useSend();
  const submit = (e: SyntheticEvent) => {
    e.preventDefault();
    void send(() => answer(step.id, "")).then((ok) => {
      if (ok) onAnswered({ title: step.title, shown: null });
    });
  };
  return (
    <form onSubmit={submit}>
      <Actions pending={pending} error={error} canGoBack={canGoBack} onBack={onBack} step={step} />
    </form>
  );
}

function SelectStep({ step, onAnswered, onBack, canGoBack }: StepProps) {
  const options = step.options ?? [];
  const [value, setValue] = useState(step.default ?? options[0]?.value ?? "");
  const { pending, error, send } = useSend();
  const name = useId();
  const submit = (e: SyntheticEvent) => {
    e.preventDefault();
    const label = options.find((o) => o.value === value)?.label ?? value;
    void send(() => answer(step.id, value)).then((ok) => {
      if (ok) onAnswered({ title: step.title, shown: label });
    });
  };
  return (
    <form onSubmit={submit}>
      <fieldset>
        <legend className="sr-only">{step.title}</legend>
        <div className="flex flex-col divide-y divide-rule border-y border-rule">
          {options.map((o, i) => (
            <label
              key={o.value}
              className={cn(
                "group flex cursor-pointer items-start gap-4 px-1 py-4 transition-colors",
                "hover:bg-accent/40 has-[:focus-visible]:bg-accent/60",
                value === o.value && "bg-accent/50",
              )}
            >
              <span
                aria-hidden
                className="w-6 pt-0.5 text-right font-mono text-xs text-muted-foreground tabular-nums"
              >
                {String(i + 1).padStart(2, "0")}
              </span>
              <input
                type="radio"
                name={name}
                value={o.value}
                checked={value === o.value}
                onChange={() => {
                  setValue(o.value);
                }}
                className="mt-1 size-4 accent-primary"
              />
              <span className="text-sm leading-relaxed whitespace-pre-wrap">{o.label}</span>
            </label>
          ))}
        </div>
      </fieldset>
      <Actions pending={pending} error={error} canGoBack={canGoBack} onBack={onBack} step={step} />
    </form>
  );
}

function InputStep({ step, onAnswered, onBack, canGoBack }: StepProps) {
  const [value, setValue] = useState(step.default ?? "");
  const { pending, error, send } = useSend();
  const id = useId();
  const submit = (e: SyntheticEvent) => {
    e.preventDefault();
    void send(() => answer(step.id, value)).then((ok) => {
      if (ok) onAnswered({ title: step.title, shown: value || "—" });
    });
  };
  return (
    <form onSubmit={submit}>
      <Label htmlFor={id} className="sr-only">
        {step.title}
      </Label>
      <Input
        id={id}
        value={value}
        onChange={(e) => {
          setValue(e.target.value);
        }}
        autoComplete="off"
        spellCheck={false}
        aria-invalid={step.error ? true : undefined}
        className="h-11 font-mono"
      />
      <Actions pending={pending} error={error} canGoBack={canGoBack} onBack={onBack} step={step} />
    </form>
  );
}

function ConfirmStep({ step, onAnswered, onBack, canGoBack }: StepProps) {
  const { pending, error, send } = useSend();
  const choose = (v: "yes" | "no") => {
    void send(() => answer(step.id, v)).then((ok) => {
      if (ok) onAnswered({ title: step.title, shown: v === "yes" ? T.yes : T.no });
    });
  };
  const def = step.default === "yes" ? "yes" : "no";
  return (
    <div>
      {error && (
        <p role="alert" className="mb-3 text-sm text-destructive">
          {error}
        </p>
      )}
      <div className="flex items-center gap-3">
        {canGoBack && (
          <Button
            type="button"
            variant="ghost"
            disabled={pending}
            onClick={() =>
              void send(() => back(step.id)).then((ok) => {
                if (ok) onBack();
              })
            }
          >
            <ArrowLeft aria-hidden /> {T.back}
          </Button>
        )}
        <div className="ml-auto flex gap-3">
          <Button
            type="button"
            variant={def === "no" ? "default" : "outline"}
            disabled={pending}
            onClick={() => {
              choose("no");
            }}
            className="min-w-24"
          >
            {T.no}
          </Button>
          <Button
            type="button"
            variant={def === "yes" ? "default" : "outline"}
            disabled={pending}
            onClick={() => {
              choose("yes");
            }}
            className="min-w-24"
          >
            {T.yes}
          </Button>
        </div>
      </div>
    </div>
  );
}

function SecretStep({ step, onAnswered, onBack, canGoBack }: StepProps) {
  const ref = useRef<HTMLInputElement>(null);
  const { pending, error, send } = useSend();
  const id = useId();
  const submit = (e: SyntheticEvent) => {
    e.preventDefault();
    const el = ref.current;
    if (!el) return;
    const bytes = new TextEncoder().encode(el.value);
    // Clear the field at once: the value lives only in this request.
    el.value = "";
    const empty = bytes.length === 0;
    void send(() => answerSecret(step.id, bytes)).then((ok) => {
      bytes.fill(0);
      if (!ok) return;
      if (empty) onBack();
      else onAnswered({ title: step.title, shown: T.secretShown });
    });
  };
  return (
    <form onSubmit={submit} autoComplete="off">
      <Label htmlFor={id} className="sr-only">
        {step.title}
      </Label>
      <div className="relative">
        <KeyRound
          aria-hidden
          className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
        />
        <Input
          id={id}
          ref={ref}
          type="password"
          autoComplete="new-password"
          aria-describedby={`${id}-hint`}
          aria-invalid={step.error ? true : undefined}
          className="h-11 pl-9 font-mono"
        />
      </div>
      <p id={`${id}-hint`} className="mt-2 text-xs text-muted-foreground">
        {T.secretHint}
      </p>
      <Actions pending={pending} error={error} canGoBack={canGoBack} onBack={onBack} step={step} />
    </form>
  );
}

/** SensitiveStep is the sealed slip: the passphrase is revealed once. */
function SensitiveStep({ step, onAnswered }: StepProps) {
  const [secret, setSecret] = useState<string | null>(null);
  const [gone, setGone] = useState(false);
  const [copied, setCopied] = useState(false);
  const { pending, error, send } = useSend();
  const [revealing, setRevealing] = useState(false);

  // Drop the passphrase from memory when the step is left.
  useEffect(
    () => () => {
      setSecret(null);
    },
    [],
  );

  const doReveal = async () => {
    setRevealing(true);
    try {
      setSecret(await reveal(step.id));
    } catch (err) {
      if (err instanceof SetupApiError && err.status === 409) setGone(true);
    } finally {
      setRevealing(false);
    }
  };
  const copy = async () => {
    if (!secret) return;
    await navigator.clipboard.writeText(secret);
    setCopied(true);
  };
  const confirm = () =>
    void send(() => answer(step.id, "")).then((ok) => {
      if (!ok) return;
      setSecret(null);
      onAnswered({ title: step.title, shown: T.sensitiveShown });
    });

  return (
    <div>
      <div className="setup-slip relative overflow-hidden rounded-sm border border-border-strong/40 bg-surface px-6 py-7 shadow-sm">
        <p className="text-2xs font-semibold tracking-[0.18em] text-status-insecure-fg uppercase">
          {T.slipTitle}
        </p>
        <p className="mt-2 max-w-prose text-sm leading-relaxed text-muted-foreground">
          {T.slipLead}
        </p>
        {step.body && (
          <p className="mt-2 max-w-prose text-sm leading-relaxed whitespace-pre-wrap text-muted-foreground">
            {step.body}
          </p>
        )}
        <div className="mt-6 min-h-16 rounded-sm border border-dashed border-border-strong/50 bg-surface-sunken px-4 py-4">
          {secret ? (
            <div className="flex items-start gap-3">
              <code
                className="flex-1 font-mono text-base leading-relaxed break-all select-all"
                aria-label={T.slipTitle}
              >
                {secret}
              </code>
              <Button type="button" variant="outline" size="sm" onClick={() => void copy()}>
                {copied ? <Check aria-hidden /> : <Copy aria-hidden />}{" "}
                {copied ? T.slipCopied : T.slipCopy}
              </Button>
            </div>
          ) : gone ? (
            <p className="text-sm text-destructive">{T.slipGone}</p>
          ) : (
            <Button
              type="button"
              variant="outline"
              onClick={() => void doReveal()}
              disabled={revealing}
            >
              <Eye aria-hidden /> {revealing ? T.slipRevealing : T.slipReveal}
            </Button>
          )}
        </div>
      </div>
      {error && (
        <p role="alert" className="mt-3 text-sm text-destructive">
          {error}
        </p>
      )}
      <div className="mt-8 flex">
        <Button
          type="button"
          className="ml-auto"
          disabled={pending || (!secret && !gone)}
          onClick={confirm}
        >
          <Check aria-hidden /> {T.slipConfirm}
        </Button>
      </div>
    </div>
  );
}
