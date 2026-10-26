import type { ComponentProps } from "react";

import { cn } from "@/lib/utils";

/** The vault dial: a locked door seen head-on. */
export function LogoMark({ className, ...props }: ComponentProps<"svg">) {
  return (
    <svg
      viewBox="0 0 32 32"
      aria-hidden="true"
      className={cn("size-7 shrink-0", className)}
      {...props}
    >
      <rect x="1.5" y="1.5" width="29" height="29" rx="6.5" className="fill-foreground" />
      <circle cx="16" cy="16" r="8" fill="none" strokeWidth="2.25" className="stroke-background" />
      <path
        d="M16 8v4.5M16 19.5V24M8 16h4.5M19.5 16H24"
        strokeWidth="2.25"
        strokeLinecap="round"
        className="stroke-background"
      />
      <circle cx="16" cy="16" r="2" className="fill-primary" />
    </svg>
  );
}

export function Wordmark({ className }: { className?: string }) {
  return (
    <span className={cn("inline-flex items-center gap-2.5", className)}>
      <LogoMark />
      <span className="font-display text-[1.6rem] leading-none tracking-tight">bekup</span>
    </span>
  );
}
