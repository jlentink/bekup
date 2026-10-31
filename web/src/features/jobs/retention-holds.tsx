import { ArchiveX } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";

import { HOLD_T as T, holdSentence, type HoldInput } from "./retention-model";

/**
 * The job page's warning when retention keeps incomplete backups that no
 * keep rule would keep on its own (Job.retentionHolds, D-209): one line
 * per destination, words and an icon (never colour alone).
 */
export function RetentionHoldsAlert({
  holds,
  historyAnchor,
}: {
  holds: readonly (HoldInput & { destinationId: string })[];
  /** Id of the run history heading on the page. */
  historyAnchor: string;
}) {
  if (holds.length === 0) return null;
  return (
    <Alert
      data-testid="retention-holds"
      className="border-status-partial/45 bg-status-partial-soft text-status-partial-fg"
    >
      <ArchiveX aria-hidden="true" />
      <AlertTitle className="line-clamp-none">{T.title}</AlertTitle>
      <AlertDescription className="text-status-partial-fg">
        <ul aria-label={T.listLabel} className="flex flex-col gap-1">
          {holds.map((h) => (
            <li key={h.destinationId} className="font-medium">
              {holdSentence(h)}
            </li>
          ))}
        </ul>
        <p className="text-foreground/80">
          {T.explain}{" "}
          <a href={`#${historyAnchor}`} className="text-foreground underline underline-offset-4">
            {T.runsLink}
          </a>
        </p>
      </AlertDescription>
    </Alert>
  );
}
