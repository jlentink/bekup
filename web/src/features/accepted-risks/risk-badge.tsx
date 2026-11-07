import { useQuery } from "@apollo/client/react";
import { CircleCheck, ShieldCheck } from "lucide-react";
import { useState } from "react";

import { SecurityBadge } from "@/components/status/security-badge";
import type { SecurityFlag } from "@/components/status/status";
import { Button } from "@/components/ui/button";
import type { RiskKind } from "@/graphql/gen/graphql";
import { formatDateTime } from "@/lib/format";
import { SecurityRisksQuery } from "@/lib/operations";
import { cn } from "@/lib/utils";

import { AcceptRiskDialog } from "./accept-dialog";
import { T } from "./messages";
import { acceptanceFor } from "./model";

/**
 * The permanent badge of a weakened setting (D-001) that knows whether the
 * risk is accepted (D-228): the badge stays, muted with the end date when an
 * acceptance covers the current condition. Where the server says the caller
 * may decide, an "Accept risk" button opens the dialog. Without the risk
 * list (not loaded, failed, or the caller sees none) it is the plain badge.
 */
export function RiskBadge({
  flag,
  kind,
  subjectId,
  className,
  showAction = true,
}: {
  flag: SecurityFlag;
  kind: RiskKind;
  /** The connection or job id; omitted for instance-wide kinds. */
  subjectId?: string;
  className?: string;
  showAction?: boolean;
}) {
  const { data } = useQuery(SecurityRisksQuery, { fetchPolicy: "cache-and-network" });
  const risk = acceptanceFor(data?.securityRisks, kind, subjectId);
  const [open, setOpen] = useState(false);
  const acceptance = risk?.acceptance;
  return (
    <span className={cn("inline-flex flex-wrap items-center gap-2", className)}>
      <SecurityBadge
        flag={flag}
        accepted={acceptance ? { until: acceptance.expiresAt ?? null } : undefined}
      />
      {showAction && risk && !acceptance && risk.canAccept ? (
        <Button
          size="sm"
          variant="ghost"
          className="h-6 px-2 text-xs"
          onClick={() => {
            setOpen(true);
          }}
        >
          <ShieldCheck aria-hidden="true" /> {T.accept}
        </Button>
      ) : null}
      {open && risk ? (
        <AcceptRiskDialog
          risk={risk}
          onOpenChange={(o) => {
            if (!o) setOpen(false);
          }}
        />
      ) : null}
    </span>
  );
}

/**
 * The decision part alone, for places with their own weakened-setting badge
 * (SMTP, the configuration page): "Accepted until …" when an acceptance
 * covers the risk, else an "Accept risk" button where the caller may decide,
 * else nothing.
 */
export function RiskDecision({ kind, subjectId }: { kind: RiskKind; subjectId?: string }) {
  const { data } = useQuery(SecurityRisksQuery, { fetchPolicy: "cache-and-network" });
  const risk = acceptanceFor(data?.securityRisks, kind, subjectId);
  const [open, setOpen] = useState(false);
  if (!risk) return null;
  const acceptance = risk.acceptance;
  return (
    <>
      {acceptance ? (
        <span
          data-testid="risk-accepted"
          className="hatch-accepted inline-flex h-6 items-center gap-1.5 rounded-sm border border-dashed border-status-insecure/35 px-2 text-2xs font-medium text-muted-foreground"
        >
          <CircleCheck aria-hidden="true" className="size-3.5" />
          {acceptance.expiresAt
            ? T.acceptedUntil(formatDateTime(acceptance.expiresAt))
            : T.acceptedForever}
        </span>
      ) : risk.canAccept ? (
        <Button
          size="sm"
          variant="ghost"
          className="h-6 px-2 text-xs"
          onClick={() => {
            setOpen(true);
          }}
        >
          <ShieldCheck aria-hidden="true" /> {T.accept}
        </Button>
      ) : null}
      {open ? (
        <AcceptRiskDialog
          risk={risk}
          onOpenChange={(o) => {
            if (!o) setOpen(false);
          }}
        />
      ) : null}
    </>
  );
}
