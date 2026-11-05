import { useCallback, useId, useState, type SyntheticEvent, type ReactNode } from "react";
import { Download, LoaderCircle, Power, ShieldCheck } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

import { finish, SetupApiError, signIn, zipURL, type Status } from "./api";
import { T } from "./messages";
import { StepView, type Entry } from "./steps";
import { useSetup } from "./use-setup";

/** codeFromFragment reads #code=... once and removes it from history. */
function codeFromFragment(): string {
  const m = /(?:^#|&)code=([A-Za-z2-7 -]+)/.exec(window.location.hash);
  if (window.location.hash) {
    window.history.replaceState(null, "", window.location.pathname + window.location.search);
  }
  return m?.[1] ?? "";
}

// Read once at load (the fragment is removed from history right away).
const fragmentCode = codeFromFragment();

export function SetupApp() {
  const [ledger, setLedger] = useState<Entry[]>([]);
  // A refused answer comes back as the same screen with an error: its
  // ledger entry goes.
  const { session, status, refresh, setSession } = useSetup(
    useCallback(() => {
      setLedger((l) => l.slice(0, -1));
    }, []),
  );
  const [stopped, setStopped] = useState(false);

  return (
    <div className="min-h-dvh bg-background text-foreground">
      <header className="border-b border-rule">
        <div className="mx-auto flex max-w-6xl items-baseline gap-3 px-6 py-5">
          <span className="font-display text-2xl italic">{T.product}</span>
          <span className="text-2xs font-semibold tracking-[0.2em] text-muted-foreground uppercase">
            {T.title}
          </span>
        </div>
      </header>
      <main className="mx-auto grid max-w-6xl gap-10 px-6 py-10 md:grid-cols-[16rem_1fr] md:gap-16 md:py-16">
        <Ledger entries={ledger} />
        <section aria-live="polite" className="min-w-0">
          {stopped ? (
            <Done title={T.doneTitle}>
              <p className="text-muted-foreground">{T.finished}</p>
            </Done>
          ) : session === "unknown" ? (
            <Working />
          ) : session === "signed-out" ? (
            <SignIn
              onSignedIn={() => {
                setSession("unknown");
                refresh();
              }}
            />
          ) : session === "lost" ? (
            <Lost onRetry={refresh} />
          ) : status ? (
            <Flow
              status={status}
              canGoBack={ledger.length > 0}
              onAnswered={(e) => {
                setLedger((l) => [...l, e]);
              }}
              onBack={() => {
                setLedger((l) => l.slice(0, -1));
              }}
              onFinished={() => {
                setStopped(true);
              }}
            />
          ) : (
            <Working />
          )}
        </section>
      </main>
    </div>
  );
}

function Ledger({ entries: all }: { entries: Entry[] }) {
  const entries = all.filter((e): e is Entry & { shown: string } => e.shown !== null);
  return (
    <aside aria-label={T.ledgerHeading} className="order-last md:order-first">
      <h2 className="text-2xs font-semibold tracking-[0.2em] text-muted-foreground uppercase">
        {T.ledgerHeading}
      </h2>
      {entries.length === 0 ? (
        <p className="mt-4 text-sm text-muted-foreground">{T.ledgerEmpty}</p>
      ) : (
        <ol className="mt-4 border-l border-rule">
          {entries.map((e, i) => (
            <li key={`${i}-${e.title}`} className="relative py-2 pl-5">
              <span
                aria-hidden
                className="absolute top-3.5 -left-[3px] size-1.5 rounded-full bg-border-strong"
              />
              <span className="block font-mono text-2xs text-muted-foreground tabular-nums">
                {String(i + 1).padStart(2, "0")}
              </span>
              <span className="block text-sm leading-snug">{e.title}</span>
              <span className="block truncate font-mono text-xs text-muted-foreground">
                {e.shown}
              </span>
            </li>
          ))}
        </ol>
      )}
    </aside>
  );
}

function Flow({
  status,
  canGoBack,
  onAnswered,
  onBack,
  onFinished,
}: {
  status: Status;
  canGoBack: boolean;
  onAnswered: (e: Entry) => void;
  onBack: () => void;
  onFinished: () => void;
}) {
  if (status.phase === "questions") {
    return status.step ? (
      <StepView
        key={status.step.id}
        step={status.step}
        canGoBack={canGoBack}
        onAnswered={onAnswered}
        onBack={onBack}
      />
    ) : (
      <Working report={status.report} />
    );
  }
  const failed = status.phase === "failed";
  return (
    <Done title={failed ? T.failedTitle : T.doneTitle}>
      {failed && status.error && (
        <Alert variant="destructive" role="alert">
          <AlertTitle>{T.failedTitle}</AlertTitle>
          <AlertDescription className="whitespace-pre-wrap">{status.error}</AlertDescription>
        </Alert>
      )}
      <Report text={status.report} />
      <div className="flex flex-col gap-4 border-t border-rule pt-6 sm:flex-row sm:items-start">
        {status.zip && (
          <div className="flex-1">
            <Button asChild variant="outline">
              <a href={zipURL} download>
                <Download aria-hidden /> {T.zip}
              </a>
            </Button>
            <p className="mt-2 max-w-md text-xs text-muted-foreground">{T.zipHint}</p>
            {status.zipNotes && status.zipNotes.length > 0 && (
              <div className="mt-3 max-w-md">
                <p className="text-xs font-semibold">{T.zipNotesHeading}</p>
                <ul className="mt-1 list-disc pl-4 text-xs text-muted-foreground">
                  {status.zipNotes.map((n) => (
                    <li key={n}>{n}</li>
                  ))}
                </ul>
              </div>
            )}
          </div>
        )}
        <FinishButton onFinished={onFinished} />
      </div>
    </Done>
  );
}

function FinishButton({ onFinished }: { onFinished: () => void }) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  return (
    <div className="sm:ml-auto">
      <Button
        disabled={pending}
        onClick={() => {
          setPending(true);
          finish()
            .then(onFinished)
            .catch((err: unknown) => {
              setError(err instanceof SetupApiError ? err.message : String(err));
              setPending(false);
            });
        }}
      >
        <Power aria-hidden /> {T.finish}
      </Button>
      {error && (
        <p role="alert" className="mt-2 text-sm text-destructive">
          {error}
        </p>
      )}
    </div>
  );
}

function Done({ title, children }: { title: string; children: ReactNode }) {
  return (
    <article className="setup-rise flex flex-col gap-6">
      <h2 className="font-display text-display tracking-tight">{title}</h2>
      {children}
    </article>
  );
}

function Report({ text }: { text: string }) {
  if (!text.trim()) return null;
  return (
    <section aria-label={T.reportHeading}>
      <h3 className="text-2xs font-semibold tracking-[0.2em] text-muted-foreground uppercase">
        {T.reportHeading}
      </h3>
      <pre className="mt-3 max-h-[28rem] overflow-auto rounded-sm border border-rule bg-surface-sunken p-4 font-mono text-xs leading-relaxed whitespace-pre-wrap">
        {text.trim()}
      </pre>
    </section>
  );
}

function Working({ report }: { report?: string }) {
  return (
    <div className="setup-rise flex flex-col gap-6">
      <div className="flex items-center gap-3">
        <LoaderCircle aria-hidden className="size-5 animate-spin text-primary" />
        <h2 className="font-display text-3xl">{T.working}</h2>
      </div>
      <p className="max-w-prose text-sm text-muted-foreground">{T.workingLead}</p>
      {report && <Report text={report} />}
    </div>
  );
}

function Lost({ onRetry }: { onRetry: () => void }) {
  return (
    <div className="setup-rise flex flex-col items-start gap-4">
      <p className="max-w-prose text-muted-foreground">{T.lost}</p>
      <Button variant="outline" onClick={onRetry}>
        {T.retry}
      </Button>
    </div>
  );
}

function SignIn({ onSignedIn }: { onSignedIn: () => void }) {
  const [code, setCode] = useState(fragmentCode);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const id = useId();

  const submit = (e: SyntheticEvent) => {
    e.preventDefault();
    setPending(true);
    setError(null);
    signIn(code)
      .then(() => {
        setCode("");
        onSignedIn();
      })
      .catch((err: unknown) => {
        setError(err instanceof SetupApiError ? err.message : String(err));
        setPending(false);
      });
  };

  return (
    <article className="setup-rise max-w-xl">
      <h2 className="font-display text-display-lg tracking-tight">{T.signInTitle}</h2>
      <p className="mt-5 leading-relaxed text-muted-foreground">{T.signInLead}</p>
      <div className="mt-6 flex gap-3 border-l-2 border-status-insecure py-1 pl-4">
        <ShieldCheck aria-hidden className="mt-0.5 size-5 shrink-0 text-status-insecure-fg" />
        <p className="text-sm leading-relaxed">{T.fingerprintCheck}</p>
      </div>
      <form onSubmit={submit} className="mt-8">
        <Label htmlFor={id}>{T.codeLabel}</Label>
        <Input
          id={id}
          value={code}
          onChange={(e) => {
            setCode(e.target.value);
          }}
          autoComplete="one-time-code"
          spellCheck={false}
          autoCapitalize="characters"
          aria-describedby={`${id}-hint`}
          aria-invalid={error ? true : undefined}
          className="mt-2 h-12 font-mono text-base tracking-[0.12em] uppercase"
        />
        <p id={`${id}-hint`} className="mt-2 text-xs text-muted-foreground">
          {T.codeHint}
        </p>
        {error && (
          <p role="alert" className="mt-3 text-sm text-destructive">
            {error}
          </p>
        )}
        <Button type="submit" size="lg" className="mt-6" disabled={pending || !code.trim()}>
          {pending ? T.signingIn : T.signIn}
        </Button>
      </form>
    </article>
  );
}
