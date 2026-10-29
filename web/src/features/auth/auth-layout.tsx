import type { ReactNode } from "react";

import { Wordmark } from "@/components/brand/logo";
import { InsecureTransportBanner } from "@/components/shell/insecure-transport-banner";
import { ThemeToggle } from "@/components/theme-toggle";
import { cn } from "@/lib/utils";

const GUARANTEES = [
  ["Encrypted", "age, per-job keys, on by default"],
  ["Signed", "ed25519 manifests, verified before restore"],
  ["Isolated", "only the runner can reach Docker"],
  ["Guarded", "a fresh second factor for every restore"],
] as const;

/** Ledger step markers of the sign-in: password → second factor → in. */
const STEPS = ["Password", "Second factor", "Signed in"] as const;

export interface AuthLayoutProps {
  eyebrow: string;
  title: string;
  lead?: ReactNode;
  children: ReactNode;
  /** Under the form (small print). */
  footer?: ReactNode;
  /** Right of the theme toggle (e.g. a sign-out button in forced flows). */
  actions?: ReactNode;
  /** Wider form column (enrollment). */
  wide?: boolean;
  /** Highlights a step of the sign-in sequence (0-based). */
  step?: number;
}

/**
 * Frame of the pages outside the app: the dark "vault" brand panel on the
 * left (in either theme) and the form column on the right.
 */
export function AuthLayout({
  eyebrow,
  title,
  lead,
  children,
  footer,
  actions,
  wide = false,
  step,
}: AuthLayoutProps) {
  return (
    <div className="flex min-h-dvh flex-col">
      <InsecureTransportBanner />
      <div className="grid flex-1 lg:grid-cols-[minmax(0,1.1fr)_minmax(0,1fr)]">
        <section
          aria-label="About bekup"
          className="dark relative hidden flex-col justify-between overflow-hidden bg-background p-12 text-foreground lg:flex"
        >
          <div className="ledger-grid pointer-events-none absolute inset-0 [mask-image:radial-gradient(ellipse_at_30%_40%,black,transparent_75%)] opacity-70" />
          <Wordmark className="relative" />
          <div className="relative flex flex-col gap-10">
            <p className="font-display text-display-lg tracking-tight">
              Every backup,
              <br />
              <em className="text-primary">accounted for.</em>
            </p>
            <dl className="grid max-w-md grid-cols-[auto_1fr] border-t border-rule">
              {GUARANTEES.map(([term, detail]) => (
                <div
                  key={term}
                  className="col-span-2 grid grid-cols-subgrid border-b border-rule py-2.5"
                >
                  <dt className="eyebrow pr-6 text-foreground">{term}</dt>
                  <dd className="text-sm text-muted-foreground">{detail}</dd>
                </div>
              ))}
            </dl>
          </div>
          <p className="relative font-mono text-xs text-muted-foreground">
            v{__BEKUP_VERSION__} · self-hosted
          </p>
        </section>

        <section className="flex flex-col p-6 sm:p-10">
          <div className="flex items-center justify-between gap-3">
            <Wordmark className="lg:invisible" />
            <div className="flex items-center gap-2">
              <ThemeToggle />
              {actions}
            </div>
          </div>
          <main
            id="main"
            className={cn(
              "mx-auto flex w-full flex-1 flex-col justify-center gap-8 py-12",
              wide ? "max-w-xl" : "max-w-sm",
            )}
          >
            {step !== undefined ? (
              <ol aria-label="Sign-in steps" className="flex items-center gap-2">
                {STEPS.map((label, i) => (
                  <li
                    key={label}
                    aria-current={i === step ? "step" : undefined}
                    className="flex items-center gap-2"
                  >
                    <span
                      className={cn(
                        "font-mono text-2xs tracking-[0.12em] uppercase",
                        i === step ? "text-foreground" : "text-muted-foreground",
                      )}
                    >
                      <span className={cn(i < step && "text-status-success-fg")}>
                        {String(i + 1).padStart(2, "0")}
                      </span>{" "}
                      {label}
                    </span>
                    {i < STEPS.length - 1 ? (
                      <span aria-hidden="true" className="h-px w-5 bg-border-strong/50" />
                    ) : null}
                  </li>
                ))}
              </ol>
            ) : null}
            <div className="flex flex-col gap-2">
              <p className="eyebrow">{eyebrow}</p>
              <h1 className="font-display text-display leading-none">{title}</h1>
              {lead ? <p className="mt-1 text-sm text-muted-foreground">{lead}</p> : null}
            </div>
            {children}
          </main>
          {footer ? (
            <p className="mx-auto max-w-sm text-center text-xs text-muted-foreground">{footer}</p>
          ) : null}
        </section>
      </div>
    </div>
  );
}
