import { Copy, EyeOff, KeyRound, TimerReset } from "lucide-react";
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
import { formatDateTime, formatRelative } from "@/lib/format";
import { messages } from "@/lib/messages";

import { usersMessages as M } from "./messages";

/** A temporary password as returned once by createUser / resetUserPassword. */
export interface IssuedPassword {
  email: string;
  password: string;
  expiresAt: string;
  kind: "created" | "reset";
}

/**
 * Shows a temporary password exactly once (D-110). The value only lives in
 * the caller's state (the mutations run with "no-cache"), so after onDone
 * clears it nothing can render it again. Closing needs an explicit
 * acknowledgement; Escape and outside clicks are ignored until then.
 */
export function TemporaryPasswordDialog({
  issued,
  onDone,
}: {
  issued: IssuedPassword | null;
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
                {M.tempEyebrow}
              </p>
              <DialogTitle className="font-display text-2xl font-normal break-all">
                {issued.kind === "created"
                  ? M.tempTitleCreated(issued.email)
                  : M.tempTitleReset(issued.email)}
              </DialogTitle>
              <DialogDescription>{M.tempLead}</DialogDescription>
            </DialogHeader>

            <figure className="relative flex flex-col gap-3 rounded-md border border-dashed border-border-strong/70 bg-surface-sunken px-5 pt-5 pb-4">
              <figcaption className="eyebrow">{M.tempPasswordLabel}</figcaption>
              <output
                aria-label={M.tempPasswordLabel}
                className="font-mono text-xl leading-snug tracking-wider break-all select-all"
              >
                {issued.password}
              </output>
              <div className="flex flex-wrap items-center justify-between gap-2 border-t border-dashed border-rule pt-3">
                <p className="flex items-center gap-1.5 font-mono text-2xs text-muted-foreground">
                  <TimerReset aria-hidden="true" className="size-3.5" />
                  {M.tempExpires(
                    formatDateTime(issued.expiresAt),
                    formatRelative(issued.expiresAt),
                  )}
                </p>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    navigator.clipboard.writeText(issued.password).then(
                      () => toast.success(M.tempCopied),
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
              {M.tempWarning}
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
                {M.tempAcknowledge}
              </label>
              <Button disabled={!ack} onClick={finish}>
                {M.tempDone}
              </Button>
            </DialogFooter>
          </>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}
