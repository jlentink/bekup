import { ExternalLink } from "lucide-react";
import type { ReactNode } from "react";

/** An external documentation link (opens a new tab, says so to screen readers). */
export function DocLink({ href, children }: { href: string; children: ReactNode }) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      className="inline-flex items-center gap-1 font-medium text-primary underline-offset-4 hover:underline"
    >
      {children}
      <ExternalLink aria-hidden="true" className="size-3" />
      <span className="sr-only"> (opens in a new tab)</span>
    </a>
  );
}

/** A shell command to run on the host: mono, selectable, prompt glyph. */
export function Command({ children }: { children: string }) {
  return (
    <pre className="overflow-x-auto rounded-md border bg-surface-sunken px-3.5 py-2.5 font-mono text-xs leading-relaxed select-all">
      <span aria-hidden="true" className="text-muted-foreground select-none">
        ${" "}
      </span>
      {children}
    </pre>
  );
}

export const textareaClass =
  "w-full rounded-md border border-input bg-surface px-3 py-2 font-mono text-xs shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 aria-invalid:border-destructive dark:bg-surface-sunken";
