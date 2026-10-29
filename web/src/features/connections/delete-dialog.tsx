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

import { jobsInUse } from "./delete-dialog-utils";

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
  /** "connection", "destination". */
  kind: string;
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
          jobs: e?.code === ApiCode.conflict ? jobsInUse(e.message) : [],
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
          <DialogTitle>
            Delete the {kind} &ldquo;{name}&rdquo;?
          </DialogTitle>
          <DialogDescription>
            Its stored credentials become unusable and its name can be reused. Existing backups and
            the audit history are kept. A {kind} that a job still uses cannot be deleted.
          </DialogDescription>
        </DialogHeader>
        {error ? (
          <Alert variant="destructive">
            <AlertTitle>{error.jobs.length > 0 ? `Still in use` : "Not deleted"}</AlertTitle>
            <AlertDescription>
              {error.jobs.length > 0 ? (
                <>
                  <p>Move or delete these jobs first:</p>
                  <ul aria-label="Jobs using it" className="list-disc pl-5">
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
            Cancel
          </Button>
          <Button type="button" variant="destructive" disabled={busy} onClick={() => void run()}>
            {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
            Delete {kind}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
