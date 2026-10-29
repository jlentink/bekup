import { useMutation } from "@apollo/client/react";
import { Copy, KeyRound, Loader2 } from "lucide-react";
import { useId, useState, type SyntheticEvent } from "react";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { CodeInput } from "@/features/auth/code-input";
import { TOTP_DIGITS } from "@/features/auth/code-input-utils";
import { reportActionError, useStepUp } from "@/features/auth/use-step-up";
import type { BeginTotpEnrollmentMutation } from "@/graphql/gen/graphql";
import { ApiCode, firstApiError, userMessage } from "@/lib/api-error";
import { formatDateTime } from "@/lib/format";
import { messages } from "@/lib/messages";
import { parseQRCodeSvg } from "@/lib/qr-svg";

import {
  BeginTotpEnrollmentMutation as BeginTotp,
  ConfirmTotpEnrollmentMutation,
  DisableTotpMutation,
  TwoFactorOverviewQuery,
} from "./operations";
import { RecoveryCodesDialog } from "./recovery-codes";
import { Section, StatePill } from "./section";
import { groupKey } from "./model";

const M = messages.totp;

type Enrollment = BeginTotpEnrollmentMutation["beginTotpEnrollment"];

/** The server's QR document re-rendered as React SVG (see lib/qr-svg). */
export function QRCode({ svg }: { svg: string }) {
  const shape = parseQRCodeSvg(svg);
  if (!shape) {
    return <p className="text-sm text-muted-foreground">{M.qrUnavailable}</p>;
  }
  return (
    <svg
      viewBox={`0 0 ${shape.size} ${shape.size}`}
      shapeRendering="crispEdges"
      role="img"
      aria-label={M.scan}
      className="size-44 rounded-sm border bg-white"
    >
      <rect width={shape.size} height={shape.size} fill="#ffffff" />
      <path fill="#000000" d={shape.path} />
    </svg>
  );
}

function EnrollmentPanel({
  enrollment,
  onConfirmed,
  onCancel,
}: {
  enrollment: Enrollment;
  onConfirmed: (codes: string[]) => void;
  onCancel: () => void;
}) {
  const { withStepUp } = useStepUp();
  // "no-cache": the recovery codes must never enter the Apollo cache.
  const [confirm] = useMutation(ConfirmTotpEnrollmentMutation, {
    fetchPolicy: "no-cache",
    refetchQueries: [TwoFactorOverviewQuery],
  });
  const [code, setCode] = useState("");
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);
  const codeId = useId();
  const errorId = useId();

  const submit = async (e: SyntheticEvent) => {
    e.preventDefault();
    if (code.length !== TOTP_DIGITS) {
      setError(messages.secondFactor.totpInvalidFormat);
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      const r = await withStepUp(() => confirm({ variables: { code } }));
      onConfirmed(r.data?.confirmTotpEnrollment.codes ?? []);
    } catch (err) {
      const first = firstApiError(err);
      if (first?.code === ApiCode.validation || first?.code === ApiCode.rateLimited) {
        setError(userMessage(err));
      } else {
        reportActionError(err);
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="grid gap-6 rounded-md border bg-surface p-5 sm:grid-cols-[auto_minmax(0,1fr)]">
      <div className="flex flex-col items-center gap-2">
        <QRCode svg={enrollment.qrCodeSvg} />
        <p className="eyebrow">{M.scanShort}</p>
      </div>
      <div className="flex min-w-0 flex-col gap-4">
        <div className="flex flex-col gap-1.5">
          <p className="text-sm font-medium">{M.manual}</p>
          <div className="flex items-center gap-2">
            <code className="min-w-0 flex-1 rounded-sm border bg-surface-sunken px-3 py-2 font-mono text-sm tracking-wider break-all select-all">
              {groupKey(enrollment.manualEntryKey)}
            </code>
            <Button
              variant="outline"
              size="icon-sm"
              aria-label={messages.common.copy}
              onClick={() => {
                navigator.clipboard.writeText(enrollment.manualEntryKey).then(
                  () => toast.success(messages.common.copied),
                  () => toast.error(messages.errors.generic),
                );
              }}
            >
              <Copy aria-hidden="true" />
            </Button>
          </div>
          <p className="font-mono text-2xs text-muted-foreground">{M.manualDetails}</p>
          <p className="text-xs text-status-partial-fg">{M.shownOnce}</p>
        </div>
        <form onSubmit={(e) => void submit(e)} noValidate className="flex flex-col gap-3">
          <Label htmlFor={codeId}>{M.confirmLabel}</Label>
          <CodeInput
            id={codeId}
            value={code}
            onChange={setCode}
            disabled={busy}
            aria-invalid={error ? true : undefined}
            aria-describedby={error ? errorId : undefined}
          />
          {error ? (
            <p id={errorId} className="text-sm text-destructive">
              {error}
            </p>
          ) : null}
          <p className="text-xs text-muted-foreground">
            {M.expires(formatDateTime(enrollment.expiresAt))}
          </p>
          <div className="flex flex-wrap gap-2">
            <Button type="submit" disabled={busy}>
              {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
              {M.confirm}
            </Button>
            <Button type="button" variant="ghost" disabled={busy} onClick={onCancel}>
              {messages.common.cancel}
            </Button>
          </div>
        </form>
      </div>
    </div>
  );
}

export interface TotpSectionProps {
  enabled: boolean;
  enabledAt: string | null | undefined;
  pending: boolean;
  account: string;
  /** Called once TOTP is on and the recovery codes were acknowledged. */
  onEnabled?: () => void;
}

/** Authenticator app (TOTP): enroll with QR or key, confirm, turn off. */
export function TotpSection({ enabled, enabledAt, pending, account, onEnabled }: TotpSectionProps) {
  const { withStepUp } = useStepUp();
  // "no-cache": the TOTP secret and otpauth URI must never enter the Apollo cache.
  const [begin, { loading: beginning }] = useMutation(BeginTotp, { fetchPolicy: "no-cache" });
  const [disable] = useMutation(DisableTotpMutation, { refetchQueries: [TwoFactorOverviewQuery] });
  const [enrollment, setEnrollment] = useState<Enrollment | null>(null);
  const [codes, setCodes] = useState<string[] | null>(null);
  const [confirmingOff, setConfirmingOff] = useState(false);

  const start = async () => {
    try {
      const r = await withStepUp(() => begin());
      setEnrollment(r.data?.beginTotpEnrollment ?? null);
    } catch (err) {
      reportActionError(err);
    }
  };

  return (
    <Section
      icon={KeyRound}
      title={M.heading}
      lead={M.lead}
      status={
        <StatePill on={enabled}>
          {enabled
            ? enabledAt
              ? M.enabledSince(formatDateTime(enabledAt))
              : M.on
            : pending
              ? M.pending
              : M.notEnabled}
        </StatePill>
      }
    >
      {enabled ? (
        <Button
          variant="outline"
          size="sm"
          className="self-start"
          onClick={() => {
            setConfirmingOff(true);
          }}
        >
          {M.disable}
        </Button>
      ) : enrollment ? (
        <EnrollmentPanel
          enrollment={enrollment}
          onCancel={() => {
            setEnrollment(null);
          }}
          onConfirmed={(c) => {
            // Drop the key from memory as soon as it is confirmed.
            setEnrollment(null);
            setCodes(c);
            toast.success(M.enabled);
          }}
        />
      ) : (
        <Button className="self-start" size="sm" disabled={beginning} onClick={() => void start()}>
          {beginning ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
          {pending ? M.restart : M.enroll}
        </Button>
      )}
      <ConfirmDialog
        open={confirmingOff}
        onOpenChange={setConfirmingOff}
        title={M.disableTitle}
        description={M.disableLead}
        confirmLabel={M.disable}
        destructive
        onConfirm={async () => {
          try {
            await withStepUp(() => disable());
            toast.success(M.disabled);
          } catch (err) {
            reportActionError(err);
            throw err;
          }
        }}
      />
      <RecoveryCodesDialog
        codes={codes}
        account={account}
        onDone={() => {
          setCodes(null);
          onEnabled?.();
        }}
      />
    </Section>
  );
}
