import { messages } from "@/lib/messages";
import { MIN_ACCEPTED_SCORE, type StrengthEstimate } from "@/lib/password-strength";
import { cn } from "@/lib/utils";

const M = messages.password;

const SEGMENT_COLOR = [
  "bg-status-failed",
  "bg-status-failed",
  "bg-status-partial",
  "bg-status-success",
  "bg-status-success",
] as const;

/**
 * Five ledger segments plus a label and the most useful hint. Advisory
 * only: the server decides (its refusal is shown on the field).
 */
export function StrengthMeter({
  estimate,
  id,
}: {
  estimate: StrengthEstimate | null;
  id?: string;
}) {
  const score = estimate?.score ?? -1;
  const label = estimate ? M.strength[estimate.score] : undefined;
  const hint = !estimate
    ? M.strengthNote
    : estimate.containsAccount
      ? M.containsAccount
      : estimate.score >= MIN_ACCEPTED_SCORE
        ? M.strengthNote
        : estimate.hints[0]
          ? M.hints[estimate.hints[0]]
          : M.strengthNote;
  return (
    <div id={id} className="flex flex-col gap-1.5" data-slot="strength-meter">
      <div
        role="meter"
        aria-label={M.strengthLabel}
        aria-valuemin={0}
        aria-valuemax={4}
        aria-valuenow={Math.max(score, 0)}
        aria-valuetext={label ?? M.strength[0]}
        className="grid grid-cols-5 gap-1"
      >
        {Array.from({ length: 5 }, (_, i) => (
          <span
            key={i}
            className={cn(
              "h-1 rounded-full bg-border transition-colors duration-200",
              i <= score && SEGMENT_COLOR[Math.max(score, 0)],
            )}
          />
        ))}
      </div>
      <p className="flex flex-wrap items-baseline justify-between gap-x-3 text-xs text-muted-foreground">
        <span>{hint}</span>
        {label ? (
          <span className="font-mono text-2xs tracking-[0.08em] text-foreground uppercase">
            {label}
          </span>
        ) : null}
      </p>
    </div>
  );
}
