import { useMutation } from "@apollo/client/react";
import { Loader2, ShieldOff } from "lucide-react";
import { useId, useState } from "react";
import { toast } from "sonner";

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
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { isStepUpCancelled, useStepUp } from "@/features/auth/use-step-up";
import type { ConnectionTlsMode } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { cn } from "@/lib/utils";

import { TLS_MODE, TLS_MODES, weakensTls } from "./labels";
import { ConnectionDetailQuery, SetTlsModeMutation } from "./operations";

export interface TlsModeDialogProps {
  id: string;
  name: string;
  current: ConnectionTlsMode;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

/**
 * Changes the TLS mode (setTlsMode, step-up). Weakening it is an audited
 * security opt-out that raises an alert and a permanent badge (D-001), so
 * the dialog spells out the consequence and asks for the connection name.
 */
export function TlsModeDialog({ id, name, current, open, onOpenChange }: TlsModeDialogProps) {
  const { withStepUp } = useStepUp();
  const [setMode] = useMutation(SetTlsModeMutation, {
    refetchQueries: [ConnectionDetailQuery],
    awaitRefetchQueries: true,
  });
  const [mode, setModeValue] = useState<ConnectionTlsMode>(current);
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const group = useId();
  const typedId = useId();

  const weakening = weakensTls(current, mode);
  const canSubmit = !busy && mode !== current && (!weakening || typed.trim() === name);

  const close = () => {
    setModeValue(current);
    setTyped("");
    setError(undefined);
    onOpenChange(false);
  };

  const submit = async () => {
    setBusy(true);
    setError(undefined);
    try {
      await withStepUp(() => setMode({ variables: { id, mode } }));
      toast.success(
        mode === "PINNED"
          ? "TLS is pinned again. Confirm the server's certificate to use the connection."
          : `TLS mode changed to ${TLS_MODE[mode].label.toLowerCase()}.`,
      );
      close();
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
      }}
    >
      <DialogContent className="gap-5 sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>Change the TLS mode</DialogTitle>
          <DialogDescription>
            Pinned TLS is the only mode that stops someone on the network from impersonating the
            database.
          </DialogDescription>
        </DialogHeader>
        <fieldset className="flex flex-col gap-2">
          <legend className="sr-only">TLS mode</legend>
          {TLS_MODES.map((m) => {
            const id = `${group}-${m}`;
            return (
              <div
                key={m}
                className={cn(
                  "flex items-start gap-3 rounded-md border p-3",
                  mode === m && "border-primary ring-2 ring-primary/20",
                  m !== "PINNED" && mode === m && "hatch-insecure border-status-insecure/60",
                )}
              >
                <input
                  id={id}
                  type="radio"
                  name={group}
                  value={m}
                  checked={mode === m}
                  onChange={() => {
                    setModeValue(m);
                    setTyped("");
                  }}
                  className="mt-1 size-4 accent-primary"
                />
                <div className="flex flex-col gap-0.5">
                  <label htmlFor={id} className="cursor-pointer font-medium">
                    {TLS_MODE[m].label}
                    {m === current ? " (current)" : ""}
                  </label>
                  <p className="text-sm text-muted-foreground">{TLS_MODE[m].description}</p>
                </div>
              </div>
            );
          })}
        </fieldset>

        {weakening ? (
          <Alert variant="destructive" className="hatch-insecure border-status-insecure/60">
            <ShieldOff aria-hidden="true" />
            <AlertTitle>This weakens the connection&apos;s security</AlertTitle>
            <AlertDescription>
              <p>
                The change is recorded in the audit log, every admin gets a security alert, and the
                connection carries a permanent &ldquo;Insecure transport&rdquo; badge. The
                certificate pins are kept but no longer checked.
              </p>
              <div className="mt-2 grid w-full gap-2">
                <Label htmlFor={typedId} className="text-foreground">
                  Type the connection name <span className="font-mono">{name}</span> to confirm
                </Label>
                <Input
                  id={typedId}
                  value={typed}
                  autoComplete="off"
                  spellCheck={false}
                  onChange={(e) => {
                    setTyped(e.target.value);
                  }}
                />
              </div>
            </AlertDescription>
          </Alert>
        ) : mode === "PINNED" && current !== "PINNED" ? (
          <Alert>
            <AlertDescription>
              After switching, the connection needs a confirmed certificate pin before it can be
              used.
            </AlertDescription>
          </Alert>
        ) : null}

        {error ? (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        ) : null}

        <DialogFooter>
          <Button type="button" variant="outline" disabled={busy} onClick={close}>
            Cancel
          </Button>
          <Button
            type="button"
            variant={weakening ? "destructive" : "default"}
            disabled={!canSubmit}
            onClick={() => void submit()}
          >
            {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
            {weakening ? "Weaken TLS" : "Change mode"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
