import { Loader2 } from "lucide-react";
import { useState } from "react";

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
import { ApiCode, firstApiError, userMessage } from "@/lib/api-error";
import { defineMessages } from "@/lib/i18n";

type Kind = "connection" | "destination";

const T = defineMessages({
  en: {
    title: (kind: Kind, name: string) => `Delete the ${kind} “${name}”?`,
    description: (kind: Kind) =>
      `Its stored credentials become unusable and its name can be reused. Existing backups and the audit history are kept. A ${kind} that a job still uses cannot be deleted.`,
    stillInUse: "Still in use",
    notDeleted: "Not deleted",
    moveFirst: "Move or delete these jobs first:",
    jobsUsing: "Jobs using it",
    cancel: "Cancel",
    delete: (kind: Kind) => `Delete ${kind}`,
  },
  nl: {
    title: (kind: Kind, name: string) =>
      `${kind === "connection" ? "Verbinding" : "Bestemming"} „${name}” verwijderen?`,
    description: (kind: Kind) =>
      `De opgeslagen inloggegevens worden onbruikbaar en de naam kan opnieuw worden gebruikt. Bestaande back-ups en de auditgeschiedenis blijven bewaard. Een ${kind === "connection" ? "verbinding" : "bestemming"} die een taak nog gebruikt, kun je niet verwijderen.`,
    stillInUse: "Nog in gebruik",
    notDeleted: "Niet verwijderd",
    moveFirst: "Verplaats of verwijder eerst deze taken:",
    jobsUsing: "Taken die dit gebruiken",
    cancel: "Annuleren",
    delete: (kind: Kind) => `${kind === "connection" ? "Verbinding" : "Bestemming"} verwijderen`,
  },
});

/**
 * Confirms a soft delete. An in-use CONFLICT keeps the dialog open and
 * lists the jobs that still use the item.
 */
export function DeleteDialog({
  open,
  onOpenChange,
  kind,
  name,
  onDelete,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  kind: Kind;
  name: string;
  onDelete: () => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<{ message: string; jobs: string[] }>();

  const close = () => {
    setError(undefined);
    onOpenChange(false);
  };

  const run = async () => {
    setBusy(true);
    setError(undefined);
    try {
      await onDelete();
      close();
    } catch (err) {
      if (!isStepUpCancelled(err)) {
        const e = firstApiError(err);
        setError({
          message: userMessage(err),
          jobs: e?.code === ApiCode.conflict ? (e.jobs ?? []) : [],
        });
      }
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
      <DialogContent className="sm:max-w-md" showCloseButton={false}>
        <DialogHeader>
          <DialogTitle>{T.title(kind, name)}</DialogTitle>
          <DialogDescription>{T.description(kind)}</DialogDescription>
        </DialogHeader>
        {error ? (
          <Alert variant="destructive">
            <AlertTitle>{error.jobs.length > 0 ? T.stillInUse : T.notDeleted}</AlertTitle>
            <AlertDescription>
              {error.jobs.length > 0 ? (
                <>
                  <p>{T.moveFirst}</p>
                  <ul aria-label={T.jobsUsing} className="list-disc pl-5">
                    {error.jobs.map((j) => (
                      <li key={j}>{j}</li>
                    ))}
                  </ul>
                </>
              ) : (
                <p>{error.message}</p>
              )}
            </AlertDescription>
          </Alert>
        ) : null}
        <DialogFooter>
          <Button type="button" variant="outline" disabled={busy} onClick={close}>
            {T.cancel}
          </Button>
          <Button type="button" variant="destructive" disabled={busy} onClick={() => void run()}>
            {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
            {T.delete(kind)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
