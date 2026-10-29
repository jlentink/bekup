import { useMutation } from "@apollo/client/react";
import { Copy, Download, LifeBuoy } from "lucide-react";
import { useId, useState } from "react";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { reportActionError, useStepUp } from "@/features/auth/use-step-up";
import { downloadText } from "@/lib/download";
import { messages } from "@/lib/messages";

import { RegenerateRecoveryCodesMutation, TwoFactorOverviewQuery } from "./operations";
import { Section } from "./section";
import { recoveryCodesText } from "./model";

const M = messages.recovery;

/**
 * Shows a new set of recovery codes exactly once. Closing needs an explicit
 * acknowledgement; the codes live only in this component's props.
 */
export function RecoveryCodesDialog({
  codes,
  account,
  onDone,
}: {
  codes: readonly string[] | null;
  account: string;
  onDone: () => void;
}) {
  const [saved, setSaved] = useState(false);
  const checkId = useId();
  const open = codes !== null;
  const text = codes ? recoveryCodesText(codes, account, window.location.host) : "";
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o && saved) {
          setSaved(false);
          onDone();
        }
      }}
    >
      <DialogContent
        className="gap-6 sm:max-w-lg"
        showCloseButton={false}
        onEscapeKeyDown={(e) => {
          if (!saved) e.preventDefault();
        }}
        onPointerDownOutside={(e) => {
          e.preventDefault();
        }}
      >
        <DialogHeader>
          <p className="eyebrow">{M.heading}</p>
          <DialogTitle className="font-display text-2xl font-normal">{M.newCodesTitle}</DialogTitle>
          <DialogDescription>{M.newCodesLead}</DialogDescription>
        </DialogHeader>
        <ol
          aria-label={M.heading}
          className="grid grid-cols-2 gap-x-6 gap-y-1 rounded-md border border-dashed border-border-strong/60 bg-surface-sunken px-5 py-4 font-mono text-sm"
        >
          {codes?.map((code, i) => (
            <li key={code} className="flex items-baseline gap-3">
              <span aria-hidden="true" className="w-5 text-right text-2xs text-muted-foreground">
                {String(i + 1).padStart(2, "0")}
              </span>
              <span className="tracking-wider select-all">{code}</span>
            </li>
          ))}
        </ol>
        <div className="flex flex-wrap gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              navigator.clipboard.writeText(text).then(
                () => toast.success(M.copiedToast),
                () => toast.error(messages.errors.generic),
              );
            }}
          >
            <Copy aria-hidden="true" /> {messages.common.copy}
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              downloadText(M.fileName, text);
            }}
          >
            <Download aria-hidden="true" /> {messages.common.download}
          </Button>
        </div>
        <DialogFooter className="items-center sm:justify-between">
          <label htmlFor={checkId} className="flex items-center gap-2 text-sm">
            <input
              id={checkId}
              type="checkbox"
              checked={saved}
              onChange={(e) => {
                setSaved(e.target.checked);
              }}
              className="size-4 accent-primary"
            />
            {M.acknowledge}
          </label>
          <Button
            disabled={!saved}
            onClick={() => {
              setSaved(false);
              onDone();
            }}
          >
            {M.done}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** Remaining recovery codes and regeneration. */
export function RecoveryCodesSection({
  remaining,
  hasFactor,
  account,
}: {
  remaining: number;
  hasFactor: boolean;
  account: string;
}) {
  const { withStepUp } = useStepUp();
  // "no-cache": the new recovery codes must never enter the Apollo cache.
  const [regenerate] = useMutation(RegenerateRecoveryCodesMutation, {
    fetchPolicy: "no-cache",
    refetchQueries: [TwoFactorOverviewQuery],
  });
  const [confirming, setConfirming] = useState(false);
  const [codes, setCodes] = useState<string[] | null>(null);

  return (
    <Section
      icon={LifeBuoy}
      title={M.heading}
      lead={M.lead}
      status={
        hasFactor ? (
          <span className="font-mono text-xs text-muted-foreground">{M.remaining(remaining)}</span>
        ) : null
      }
    >
      {hasFactor ? (
        <Button
          variant="outline"
          size="sm"
          className="self-start"
          onClick={() => {
            setConfirming(true);
          }}
        >
          {M.regenerate}
        </Button>
      ) : (
        <p className="text-sm text-muted-foreground">{M.none}</p>
      )}
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={M.regenerateTitle}
        description={M.regenerateLead}
        confirmLabel={M.regenerate}
        onConfirm={async () => {
          try {
            const r = await withStepUp(() => regenerate());
            setCodes(r.data?.regenerateRecoveryCodes.codes ?? null);
          } catch (err) {
            reportActionError(err);
            throw err;
          }
        }}
      />
      <RecoveryCodesDialog
        codes={codes}
        account={account}
        onDone={() => {
          setCodes(null);
        }}
      />
    </Section>
  );
}
