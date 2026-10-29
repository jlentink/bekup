import { useMutation } from "@apollo/client/react";
import { useState } from "react";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { Switch } from "@/components/ui/switch";
import { reportActionError } from "@/features/auth/use-step-up";
import { formatDateTime } from "@/lib/format";

import { DisableJobMutation, EnableJobMutation } from "./operations";

const T = {
  label: (name: string) => `Scheduled runs of ${name}`,
  enableTitle: (name: string) => `Enable ${name}?`,
  enableLead: "The schedule is planned again from now. Missed periods are not caught up.",
  disableTitle: (name: string) => `Disable ${name}?`,
  disableLead:
    "No scheduled or manual runs start while the job is disabled. A run in progress finishes. Backups and keys stay.",
  enable: "Enable job",
  disable: "Disable job",
  enabled: (next: string | null) => (next ? `Job enabled. Next run ${next}.` : "Job enabled."),
  disabled: "Job disabled.",
} as const;

/**
 * The enabled switch of a job (staff). Both directions ask for a
 * confirmation first; the switch reflects the server's state only.
 */
export function JobEnabledToggle({
  job,
  disabled = false,
  showLabel = false,
}: {
  job: { id: string; name: string; enabled: boolean };
  disabled?: boolean;
  showLabel?: boolean;
}) {
  const [confirming, setConfirming] = useState(false);
  const [enable] = useMutation(EnableJobMutation);
  const [disable] = useMutation(DisableJobMutation);
  const turningOn = !job.enabled;

  return (
    <>
      <span className="inline-flex items-center gap-2">
        <Switch
          checked={job.enabled}
          disabled={disabled}
          aria-label={T.label(job.name)}
          onCheckedChange={() => {
            setConfirming(true);
          }}
        />
        {showLabel ? (
          <span className="text-xs text-muted-foreground">
            {job.enabled ? "Enabled" : "Disabled"}
          </span>
        ) : null}
      </span>
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={turningOn ? T.enableTitle(job.name) : T.disableTitle(job.name)}
        description={turningOn ? T.enableLead : T.disableLead}
        confirmLabel={turningOn ? T.enable : T.disable}
        destructive={!turningOn}
        onConfirm={async () => {
          try {
            if (turningOn) {
              const r = await enable({ variables: { id: job.id } });
              const next = r.data?.enableJob.nextRunAt ?? null;
              toast.success(T.enabled(next ? formatDateTime(next) : null));
            } else {
              await disable({ variables: { id: job.id } });
              toast.success(T.disabled);
            }
          } catch (err) {
            reportActionError(err);
            throw err;
          }
        }}
      />
    </>
  );
}
