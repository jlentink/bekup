import { KeyRound, Loader2 } from "lucide-react";
import { useId, useState } from "react";

import { CopyValue } from "@/components/copy-value";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { isStepUpCancelled } from "@/features/auth/use-step-up";
import { userMessage } from "@/lib/api-error";

export interface GeneratedKeypair {
  publicKey: string;
  fingerprintSha256: string;
}

export interface KeypairDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** "the SSH bastion", "the SFTP server". */
  subject: string;
  /** True when a stored password or key will be replaced. */
  replaces: boolean;
  onGenerate: () => Promise<GeneratedKeypair>;
}

/**
 * Generates an ed25519 keypair on the server. The private key is stored
 * encrypted and never leaves the server; the dialog shows the public
 * authorized_keys line once, to install on the server.
 */
export function KeypairDialog({
  open,
  onOpenChange,
  subject,
  replaces,
  onGenerate,
}: KeypairDialogProps) {
  const [result, setResult] = useState<GeneratedKeypair>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [installed, setInstalled] = useState(false);
  const checkId = useId();

  const close = () => {
    setResult(undefined);
    setError(undefined);
    setInstalled(false);
    onOpenChange(false);
  };

  const generate = async () => {
    setBusy(true);
    setError(undefined);
    try {
      setResult(await onGenerate());
    } catch (err) {
      if (!isStepUpCancelled(err)) setError(userMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (busy) return;
        if (!o) close();
        else onOpenChange(true);
      }}
    >
      <DialogContent className="gap-5 sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{result ? "Install the public key" : "Generate an SSH keypair"}</DialogTitle>
          <DialogDescription>
            {result
              ? `Add this line to ~/.ssh/authorized_keys of the account on ${subject}. The private key stays encrypted on the bekup server.`
              : `bekup generates an ed25519 key and stores the private half encrypted. Only the public key is shown, to install on ${subject}.`}
          </DialogDescription>
        </DialogHeader>
        {result ? (
          <div className="flex flex-col gap-4">
            <div className="flex flex-col gap-1.5">
              <p className="eyebrow">authorized_keys line</p>
              <CopyValue value={result.publicKey} label="Copy the public key" />
            </div>
            <div className="flex flex-col gap-1.5">
              <p className="eyebrow">Fingerprint</p>
              <p className="font-mono text-xs break-all">{result.fingerprintSha256}</p>
            </div>
            <label htmlFor={checkId} className="flex items-start gap-2 text-sm">
              <input
                id={checkId}
                type="checkbox"
                checked={installed}
                onChange={(e) => {
                  setInstalled(e.target.checked);
                }}
                className="mt-0.5 size-4 accent-primary"
              />
              I installed the public key (it stays visible on this page).
            </label>
          </div>
        ) : replaces ? (
          <Alert>
            <KeyRound aria-hidden="true" />
            <AlertDescription>
              The stored password or private key is replaced. Connections fail until the new public
              key is installed.
            </AlertDescription>
          </Alert>
        ) : null}
        {error ? (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        ) : null}
        <DialogFooter>
          {result ? (
            <Button type="button" disabled={!installed} onClick={close}>
              Done
            </Button>
          ) : (
            <>
              <Button type="button" variant="outline" disabled={busy} onClick={close}>
                Cancel
              </Button>
              <Button type="button" disabled={busy} onClick={() => void generate()}>
                {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
                Generate keypair
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
