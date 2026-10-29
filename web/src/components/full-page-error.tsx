import { RotateCw } from "lucide-react";

import { LogoMark } from "@/components/brand/logo";
import { Button } from "@/components/ui/button";
import { userMessage } from "@/lib/api-error";
import { messages } from "@/lib/messages";

/** Whole-page failure (e.g. the server is unreachable while loading the session). */
export function FullPageError({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  return (
    <div className="ledger-grid flex min-h-dvh items-center justify-center p-6">
      <div
        role="alert"
        className="flex max-w-md flex-col items-start gap-4 rounded-lg border bg-surface p-8 shadow-overlay"
      >
        <LogoMark />
        <p className="eyebrow">Unavailable</p>
        <h1 className="font-display text-display leading-none">Cannot load bekup</h1>
        <p className="text-sm text-muted-foreground">{userMessage(error)}</p>
        <Button variant="outline" onClick={onRetry}>
          <RotateCw aria-hidden="true" /> {messages.common.retry}
        </Button>
      </div>
    </div>
  );
}
