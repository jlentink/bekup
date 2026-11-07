import { KeyRound, Lock, LockKeyhole, TriangleAlert } from "lucide-react";

import { SecurityBadge } from "@/components/status/security-badge";
import { RiskBadge } from "@/features/accepted-risks/risk-badge";
import type { JobEncryptionMode } from "@/graphql/gen/graphql";
import { cn } from "@/lib/utils";

interface Enc {
  configured: boolean;
  mode?: JobEncryptionMode | null;
  optedOut: boolean;
}

/**
 * The encryption state of a job: passphrase, recipient, the permanent
 * "unencrypted" hazard badge for an opt-out (D-001), or "not configured"
 * (backups refuse to run).
 */
export function EncryptionBadge({
  encryption,
  className,
  jobId,
  acceptAction = false,
}: {
  encryption: Enc;
  className?: string;
  /** The job: an accepted opt-out (D-228) shows as accepted. */
  jobId?: string;
  /** Offer "Accept risk" (detail pages). */
  acceptAction?: boolean;
}) {
  if (encryption.optedOut || encryption.mode === "NONE") {
    if (jobId) {
      return (
        <RiskBadge
          flag="unencrypted"
          kind="ENCRYPTION_OPT_OUT"
          subjectId={jobId}
          showAction={acceptAction}
          {...(className ? { className } : {})}
        />
      );
    }
    return <SecurityBadge flag="unencrypted" {...(className ? { className } : {})} />;
  }
  if (!encryption.configured || !encryption.mode) {
    return (
      <span
        className={cn(
          "inline-flex h-6 items-center gap-1.5 rounded-sm border border-status-failed/40 bg-status-failed-soft px-2 text-xs font-medium text-status-failed-fg",
          className,
        )}
      >
        <TriangleAlert aria-hidden="true" className="size-3.5" />
        No key configured
      </span>
    );
  }
  const passphrase = encryption.mode === "PASSPHRASE";
  const Icon = passphrase ? KeyRound : LockKeyhole;
  return (
    <span
      className={cn(
        "inline-flex h-6 items-center gap-1.5 rounded-sm border bg-surface px-2 font-mono text-2xs font-medium tracking-[0.06em] text-muted-foreground uppercase",
        className,
      )}
      title={
        passphrase ? "Encrypted with the job's passphrase (age)" : "Encrypted to an age recipient"
      }
    >
      <Lock aria-hidden="true" className="size-3 text-status-success" />
      <Icon aria-hidden="true" className="size-3" />
      {passphrase ? "Passphrase" : "Recipient"}
    </span>
  );
}
