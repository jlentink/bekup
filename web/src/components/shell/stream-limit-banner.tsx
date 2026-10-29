import { RadioTower } from "lucide-react";
import { useSyncExternalStore } from "react";

import { messages } from "@/lib/messages";
import { streamLimit } from "@/lib/apollo";

/**
 * Shown while a live stream of this tab is refused because the session is
 * at its stream cap (429, D-197): too many tabs with live pages. The link
 * keeps retrying with a growing backoff; closing tabs frees streams.
 */
export function StreamLimitBanner() {
  const limited = useSyncExternalStore(streamLimit.subscribe, streamLimit.count, () => 0);
  if (limited === 0) return null;
  return (
    <div
      role="status"
      data-slot="stream-limit-banner"
      className="flex items-center gap-3 border-b bg-muted px-4 py-2 text-foreground sm:px-6"
    >
      <RadioTower aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
      <p className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-0.5 text-[0.8125rem]">
        <span className="font-mono text-2xs font-semibold tracking-[0.12em] uppercase">
          {messages.shell.streamLimitTitle}
        </span>
        <span>{messages.shell.streamLimit}</span>
      </p>
    </div>
  );
}
