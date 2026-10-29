import { Copy, Download, KeySquare } from "lucide-react";
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
import { downloadText } from "@/lib/download";

import type { OneTimeKey } from "./one-time-key-model";

const T = {
  eyebrow: "Shown once",
  passphraseTitle: "Save the new passphrase",
  identityTitle: "Save the private key",
  passphraseLead:
    "bekup stores this passphrase encrypted, but you need it to decrypt backups outside bekup. It is not shown again.",
  identityLead:
    "bekup keeps only the public key. Without this private key the job's backups cannot be decrypted: store it offline now. It is never shown again.",
  version: (v: number) => `Version ${String(v)}`,
  recipient: "Public key (recipient)",
  copied: "Copied to the clipboard.",
  copyFailed: "Copying failed. Select the text and copy it by hand.",
  acknowledge: "I've saved it somewhere safe",
  done: "Done",
  copy: "Copy",
  download: "Download",
} as const;

/**
 * Shows a generated passphrase or age identity once. The value lives only
 * in the caller's state and is dropped on Done; closing needs the explicit
 * acknowledgement (D-179 item 8).
 */
export function OneTimeKeyDialog({
  value,
  onDone,
}: {
  value: OneTimeKey | null;
  onDone: () => void;
}) {
  const [saved, setSaved] = useState(false);
  const checkId = useId();
  const finish = () => {
    setSaved(false);
    onDone();
  };
  const identity = value?.kind === "identity";
  return (
    <Dialog
      open={value !== null}
      onOpenChange={(o) => {
        if (!o && saved) finish();
      }}
    >
      <DialogContent
        className="gap-5 sm:max-w-xl"
        showCloseButton={false}
        onEscapeKeyDown={(e) => {
          if (!saved) e.preventDefault();
        }}
        onPointerDownOutside={(e) => {
          e.preventDefault();
        }}
      >
        <DialogHeader>
          <p className="eyebrow flex items-center gap-1.5">
            <KeySquare aria-hidden="true" className="size-3.5" /> {T.eyebrow}
            {value?.version != null ? ` · ${T.version(value.version)}` : ""}
          </p>
          <DialogTitle className="font-display text-2xl font-normal">
            {identity ? T.identityTitle : T.passphraseTitle}
          </DialogTitle>
          <DialogDescription>{identity ? T.identityLead : T.passphraseLead}</DialogDescription>
        </DialogHeader>
        <output
          aria-label={identity ? "Private key" : "Passphrase"}
          className="block rounded-md border border-dashed border-border-strong/60 bg-surface-sunken px-4 py-3.5 font-mono text-sm break-all select-all"
        >
          {value?.value}
        </output>
        {value?.recipient ? (
          <div className="flex flex-col gap-1">
            <p className="eyebrow">{T.recipient}</p>
            <p className="font-mono text-xs break-all text-muted-foreground">{value.recipient}</p>
          </div>
        ) : null}
        <div className="flex flex-wrap gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              if (!value) return;
              navigator.clipboard.writeText(value.value).then(
                () => toast.success(T.copied),
                () => toast.error(T.copyFailed),
              );
            }}
          >
            <Copy aria-hidden="true" /> {T.copy}
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              if (value) downloadText(value.file.name, value.file.content);
            }}
          >
            <Download aria-hidden="true" /> {T.download}
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
            {T.acknowledge}
          </label>
          <Button disabled={!saved} onClick={finish}>
            {T.done}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
