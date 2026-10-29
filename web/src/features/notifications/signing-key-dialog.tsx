import { BookOpen, Copy, EyeOff, KeyRound } from "lucide-react";
import { useId, useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { messages } from "@/lib/messages";

import { notificationsMessages as M } from "./messages";

/** The receiver-side signature check in the operator docs. */
export const SIGNATURE_DOCS_URL =
  "https://github.com/jlentink/bekup/blob/main/docs/guides/notifications.md#verifying-the-signature";

/**
 * A signing key returned exactly once: by addMyWebhook / rotateMyWebhookKey
 * (personal webhooks) or createSharedChannel / rotateSharedChannelKey
 * (shared channels, admin).
 */
export interface IssuedKey {
  /** The webhook's label or the shared channel's name. */
  label: string;
  /** The masked URL (scheme, host, port), when known. */
  displayUrl?: string | undefined;
  key: string;
  kind: "added" | "rotated";
}

/**
 * The one dialog that shows a webhook signing key exactly once (D-191),
 * for personal webhooks and shared channels alike. The key only lives in
 * the caller's state (the mutations run with "no-cache"); after onDone
 * clears it nothing can render it again. Closing needs an explicit
 * acknowledgement; Escape and outside clicks are ignored until then.
 */
export function SigningKeyDialog({
  issued,
  onDone,
}: {
  issued: IssuedKey | null;
  onDone: () => void;
}) {
  const [ack, setAck] = useState(false);
  const checkId = useId();
  const finish = () => {
    setAck(false);
    onDone();
  };
  return (
    <Dialog
      open={issued !== null}
      onOpenChange={(o) => {
        if (!o && ack) finish();
      }}
    >
      <DialogContent
        className="gap-6 sm:max-w-lg"
        showCloseButton={false}
        onEscapeKeyDown={(e) => {
          if (!ack) e.preventDefault();
        }}
        onPointerDownOutside={(e) => {
          e.preventDefault();
        }}
      >
        {issued ? (
          <>
            <DialogHeader className="gap-2">
              <p className="eyebrow flex items-center gap-2">
                <KeyRound aria-hidden="true" className="size-3.5" />
                {M.key.eyebrow}
              </p>
              <DialogTitle className="font-display text-2xl font-normal break-all">
                {issued.kind === "added"
                  ? M.key.titleAdded(issued.label)
                  : M.key.titleRotated(issued.label)}
              </DialogTitle>
              <DialogDescription>{M.key.lead}</DialogDescription>
            </DialogHeader>

            <figure className="flex flex-col gap-3 rounded-md border border-dashed border-border-strong/70 bg-surface-sunken px-5 pt-5 pb-4">
              <figcaption className="flex items-baseline justify-between gap-3">
                <span className="eyebrow">{M.key.label}</span>
                {issued.displayUrl ? (
                  <span className="truncate font-mono text-2xs text-muted-foreground">
                    {issued.displayUrl}
                  </span>
                ) : null}
              </figcaption>
              <output
                aria-label={M.key.label}
                data-testid="signing-key"
                className="font-mono text-sm leading-relaxed tracking-wide break-all select-all"
              >
                {issued.key}
              </output>
              <div className="flex flex-wrap items-center justify-between gap-2 border-t border-dashed border-rule pt-3">
                <a
                  href={SIGNATURE_DOCS_URL}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1.5 text-xs font-medium text-primary underline-offset-4 hover:underline"
                >
                  <BookOpen aria-hidden="true" className="size-3.5" />
                  {M.key.verifyLink}
                </a>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    navigator.clipboard.writeText(issued.key).then(
                      () => toast.success(M.key.copied),
                      () => toast.error(messages.errors.generic),
                    );
                  }}
                >
                  <Copy aria-hidden="true" /> {messages.common.copy}
                </Button>
              </div>
            </figure>

            <p
              role="note"
              className="flex gap-2.5 rounded-md border border-status-partial/40 bg-status-partial-soft px-3.5 py-2.5 text-sm text-status-partial-fg"
            >
              <EyeOff aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
              {M.key.warning}
            </p>

            <DialogFooter className="items-center sm:justify-between">
              <label htmlFor={checkId} className="flex items-center gap-2 text-sm">
                <input
                  id={checkId}
                  type="checkbox"
                  checked={ack}
                  onChange={(e) => {
                    setAck(e.target.checked);
                  }}
                  className="size-4 accent-primary"
                />
                {M.key.acknowledge}
              </label>
              <Button disabled={!ack} onClick={finish}>
                {M.key.done}
              </Button>
            </DialogFooter>
          </>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}
