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
import { defineMessages } from "@/lib/i18n";

const T = defineMessages({
  en: {
    installTitle: "Install the public key",
    generateTitle: "Generate an SSH keypair",
    installBody: (subject: string) =>
      `Add this line to ~/.ssh/authorized_keys of the account on ${subject}. The private key stays encrypted on the bekup server.`,
    generateBody: (subject: string) =>
      `bekup generates an ed25519 key and stores the private half encrypted. Only the public key is shown, to install on ${subject}.`,
    keyLine: "authorized_keys line",
    copyPublicKey: "Copy the public key",
    fingerprint: "Fingerprint",
    installed: "I installed the public key (it stays visible on this page).",
    replaces:
      "The stored password or private key is replaced. Connections fail until the new public key is installed.",
    done: "Done",
    cancel: "Cancel",
    generate: "Generate keypair",
  },
  nl: {
    installTitle: "Publieke sleutel installeren",
    generateTitle: "SSH-sleutelpaar genereren",
    installBody: (subject: string) =>
      `Voeg deze regel toe aan ~/.ssh/authorized_keys van het account op ${subject}. De privésleutel blijft versleuteld op de bekup-server.`,
    generateBody: (subject: string) =>
      `bekup genereert een ed25519-sleutel en slaat de privéhelft versleuteld op. Alleen de publieke sleutel wordt getoond, om te installeren op ${subject}.`,
    keyLine: "Regel voor authorized_keys",
    copyPublicKey: "Publieke sleutel kopiëren",
    fingerprint: "Vingerafdruk",
    installed: "Ik heb de publieke sleutel geïnstalleerd (hij blijft zichtbaar op deze pagina).",
    replaces:
      "Het opgeslagen wachtwoord of de privésleutel wordt vervangen. Verbindingen mislukken totdat de nieuwe publieke sleutel is geïnstalleerd.",
    done: "Klaar",
    cancel: "Annuleren",
    generate: "Sleutelpaar genereren",
  },
});

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
          <DialogTitle>{result ? T.installTitle : T.generateTitle}</DialogTitle>
          <DialogDescription>
            {result ? T.installBody(subject) : T.generateBody(subject)}
          </DialogDescription>
        </DialogHeader>
        {result ? (
          <div className="flex flex-col gap-4">
            <div className="flex flex-col gap-1.5">
              <p className="eyebrow">{T.keyLine}</p>
              <CopyValue value={result.publicKey} label={T.copyPublicKey} />
            </div>
            <div className="flex flex-col gap-1.5">
              <p className="eyebrow">{T.fingerprint}</p>
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
              {T.installed}
            </label>
          </div>
        ) : replaces ? (
          <Alert>
            <KeyRound aria-hidden="true" />
            <AlertDescription>{T.replaces}</AlertDescription>
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
              {T.done}
            </Button>
          ) : (
            <>
              <Button type="button" variant="outline" disabled={busy} onClick={close}>
                {T.cancel}
              </Button>
              <Button type="button" disabled={busy} onClick={() => void generate()}>
                {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
                {T.generate}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
