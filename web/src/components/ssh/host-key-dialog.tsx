import { AlertTriangle, Fingerprint, Loader2, RefreshCw } from "lucide-react";
import { useId, useState } from "react";

import { CopyValue } from "@/components/copy-value";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
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
import { formatDateTime } from "@/lib/format";

/** An SSH host key as the API returns it (public data). */
export interface HostKey {
  type: string;
  fingerprintSha256: string;
  authorizedKey: string;
  confirmedAt?: string | null;
  host?: string | null;
  port?: number | null;
}

export interface HostKeyDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** "the SSH bastion", "the SFTP server". */
  subject: string;
  /** The currently pinned key, if any (shown to compare against). */
  pinned: HostKey | null | undefined;
  /** Captures the key the server presents (trusts nothing). */
  onFetch: () => Promise<HostKey>;
  /** Pins the key, bound to the endpoint it was fetched from (D-140). */
  onConfirm: (key: HostKey, host: string, port: number) => Promise<void>;
}

/**
 * Trust on first use, done deliberately: fetch the host key, show its
 * type, fingerprint and authorized_keys line, have the admin compare it
 * out of band and confirm explicitly. The confirmation carries the host
 * and port the key was fetched from, so a since-changed record is refused.
 */
export function HostKeyDialog({
  open,
  onOpenChange,
  subject,
  pinned,
  onFetch,
  onConfirm,
}: HostKeyDialogProps) {
  const [fetched, setFetched] = useState<HostKey>();
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState<"fetch" | "confirm">();
  const [compared, setCompared] = useState(false);
  const checkId = useId();

  const reset = () => {
    setFetched(undefined);
    setError(undefined);
    setCompared(false);
    setBusy(undefined);
  };

  const fetchKey = async () => {
    setBusy("fetch");
    setError(undefined);
    setCompared(false);
    try {
      setFetched(await onFetch());
    } catch (err) {
      setError(userMessage(err));
    } finally {
      setBusy(undefined);
    }
  };

  const confirm = async () => {
    if (!fetched?.host || fetched.port == null) return;
    setBusy("confirm");
    setError(undefined);
    try {
      await onConfirm(fetched, fetched.host, fetched.port);
      reset();
      onOpenChange(false);
    } catch (err) {
      if (!isStepUpCancelled(err)) setError(userMessage(err));
      setBusy(undefined);
    }
  };

  const changed = pinned && fetched && pinned.authorizedKey !== fetched.authorizedKey;

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (busy) return;
        if (!o) reset();
        onOpenChange(o);
      }}
    >
      <DialogContent className="gap-5 sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>Confirm the host key</DialogTitle>
          <DialogDescription>
            bekup connects to {subject} without signing in and shows the key it presents. Nothing is
            trusted until you compare the fingerprint with the server out of band (for example{" "}
            <code className="font-mono text-xs">
              ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub
            </code>{" "}
            on the server) and confirm it.
          </DialogDescription>
        </DialogHeader>

        {pinned ? (
          <div className="flex flex-col gap-1.5">
            <p className="eyebrow">Pinned now</p>
            <p className="font-mono text-xs break-all">
              {pinned.type} {pinned.fingerprintSha256}
            </p>
          </div>
        ) : null}

        {!fetched ? (
          <Button
            type="button"
            variant="outline"
            className="self-start"
            disabled={busy === "fetch"}
            onClick={() => void fetchKey()}
          >
            {busy === "fetch" ? (
              <Loader2 aria-hidden="true" className="animate-spin" />
            ) : (
              <Fingerprint aria-hidden="true" />
            )}
            Fetch host key
          </Button>
        ) : (
          <section
            aria-label="Fetched host key"
            className="flex flex-col gap-4 rounded-md border border-primary/30 bg-accent/40 p-4"
          >
            <dl className="grid grid-cols-[7rem_minmax(0,1fr)] gap-x-4 gap-y-2 text-sm">
              <dt className="eyebrow pt-0.5">Fetched from</dt>
              <dd className="font-mono text-xs break-all">
                {fetched.host}:{fetched.port}
              </dd>
              <dt className="eyebrow pt-0.5">Key type</dt>
              <dd className="font-mono text-xs">{fetched.type}</dd>
            </dl>
            <div className="flex flex-col gap-1.5">
              <p className="eyebrow">Fingerprint (SHA-256)</p>
              <p
                data-testid="host-key-fingerprint"
                className="rounded-md border bg-surface px-3 py-2.5 font-mono text-sm tracking-tight break-all"
              >
                {fetched.fingerprintSha256}
              </p>
            </div>
            <div className="flex flex-col gap-1.5">
              <p className="eyebrow">Authorized key line</p>
              <CopyValue value={fetched.authorizedKey} label="Copy the host key line" />
            </div>
            {changed ? (
              <Alert variant="destructive">
                <AlertTriangle aria-hidden="true" />
                <AlertTitle>This is not the pinned key</AlertTitle>
                <AlertDescription>
                  Confirm it only if the server&apos;s key was replaced on purpose. An unexpected
                  change can mean someone is intercepting the connection.
                </AlertDescription>
              </Alert>
            ) : null}
            <label htmlFor={checkId} className="flex items-start gap-2 text-sm">
              <input
                id={checkId}
                type="checkbox"
                checked={compared}
                onChange={(e) => {
                  setCompared(e.target.checked);
                }}
                className="mt-0.5 size-4 accent-primary"
              />
              I compared this fingerprint with the server out of band and it matches.
            </label>
          </section>
        )}

        {error ? (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        ) : null}

        <DialogFooter className="sm:justify-between">
          {fetched ? (
            <Button
              type="button"
              variant="ghost"
              disabled={busy !== undefined}
              onClick={() => void fetchKey()}
            >
              <RefreshCw aria-hidden="true" /> Fetch again
            </Button>
          ) : (
            <span />
          )}
          <Button
            type="button"
            disabled={!fetched || !compared || busy !== undefined}
            onClick={() => void confirm()}
          >
            {busy === "confirm" ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
            Pin this host key
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** One line for a pinned host key: type, fingerprint and when it was confirmed. */
export function PinnedHostKey({ hostKey }: { hostKey: HostKey }) {
  return (
    <div className="flex flex-col gap-1">
      <p className="font-mono text-xs break-all">
        <span className="text-muted-foreground">{hostKey.type}</span> {hostKey.fingerprintSha256}
      </p>
      {hostKey.confirmedAt ? (
        <p className="text-xs text-muted-foreground">
          Confirmed {formatDateTime(hostKey.confirmedAt)}
        </p>
      ) : null}
    </div>
  );
}
