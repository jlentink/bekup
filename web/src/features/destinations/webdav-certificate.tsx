import { useMutation } from "@apollo/client/react";
import { AlertTriangle, Loader2, ShieldCheck, ShieldOff } from "lucide-react";
import { useId, useState } from "react";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { CopyValue } from "@/components/copy-value";
import { Tag } from "@/components/status/tag";
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
import { isStepUpCancelled, reportActionError, useStepUp } from "@/features/auth/use-step-up";
import { Fact, Facts, LedgerSection } from "@/features/connections/ui";
import { validity } from "@/features/connections/pin-model";
import type { WebdavTrust } from "@/graphql/gen/graphql";
import { ApiCode, hasApiCode, userMessage } from "@/lib/api-error";
import { formatDateTime } from "@/lib/format";
import { cn } from "@/lib/utils";

import {
  ClearWebdavCertificatePinMutation,
  DestinationDetailQuery,
  PinWebdavCertificateMutation,
} from "./operations";
import { colonHex, hexGroups, TRUST_TONE, type WebdavCert } from "./webdav-model";
import { TRUST_DESCRIPTION, TRUST_LABEL, WT } from "./webdav-text";

/**
 * The SPKI SHA-256 as a plate of 16 four-character groups: two rows of
 * eight on wide screens, so an admin can read it out and compare it in
 * chunks. The accessible text is the plain hex.
 */
export function FingerprintPlate({ hex, testId }: { hex: string; testId?: string }) {
  const groups = hexGroups(hex);
  return (
    <div
      data-testid={testId}
      className="rounded-md border border-primary/25 bg-surface px-3 py-2.5 shadow-xs"
    >
      <span className="sr-only">{hex}</span>
      <ol
        aria-hidden="true"
        className="grid grid-cols-4 gap-x-3 gap-y-1 font-mono text-sm tracking-wide sm:grid-cols-8"
      >
        {groups.map((g, i) => (
          <li key={`${String(i)}-${g}`} className={cn(i % 2 === 1 && "text-muted-foreground")}>
            {g}
          </li>
        ))}
      </ol>
    </div>
  );
}

function ValidityTag({ cert }: { cert: WebdavCert }) {
  switch (validity(cert)) {
    case "expired":
      return <Tag tone="danger">{WT.cert.expired}</Tag>;
    case "not-yet":
      return <Tag tone="danger">{WT.cert.notYet}</Tag>;
    case "7d":
    case "30d":
    case "ok":
      return null;
  }
}

/** The facts of a certificate: SPKI plate, certificate SHA-256, validity. */
export function CertificateFacts({
  cert,
  plateTestId,
}: {
  cert: WebdavCert;
  plateTestId?: string;
}) {
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-col gap-1.5">
        <p className="eyebrow">{WT.cert.spki}</p>
        <FingerprintPlate hex={cert.spkiSha256} {...(plateTestId ? { testId: plateTestId } : {})} />
      </div>
      <Facts>
        <Fact term={WT.cert.fingerprint} mono>
          {colonHex(cert.fingerprintSha256)}
        </Fact>
        <Fact term={WT.cert.valid}>
          <span className="flex flex-wrap items-center gap-2">
            <span className="text-xs">
              {WT.cert.validRange(formatDateTime(cert.notBefore), formatDateTime(cert.notAfter))}
            </span>
            <ValidityTag cert={cert} />
            <Tag tone="neutral" compact>
              {cert.selfSigned ? WT.cert.selfSigned : WT.cert.caIssued}
            </Tag>
          </span>
        </Fact>
        {cert.pinnedAt ? (
          <Fact term={WT.cert.pinnedAt}>{formatDateTime(cert.pinnedAt)}</Fact>
        ) : null}
      </Facts>
    </div>
  );
}

export interface PinCertificateDialogProps {
  id: string;
  cert: WebdavCert;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onPinned: () => void;
}

/**
 * Pins a certificate a test saw (admin): shows the fingerprints to compare
 * out of band, asks for an explicit confirmation, then calls
 * pinWebdavCertificate with the SPKI SHA-256 (step-up). The server captures
 * the certificate again and refuses (CONFLICT) if it changed meanwhile.
 */
export function PinCertificateDialog({
  id,
  cert,
  open,
  onOpenChange,
  onPinned,
}: PinCertificateDialogProps) {
  const { withStepUp } = useStepUp();
  const [pin] = useMutation(PinWebdavCertificateMutation, {
    refetchQueries: [DestinationDetailQuery],
    awaitRefetchQueries: true,
  });
  const [compared, setCompared] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const checkId = useId();

  const reset = () => {
    setCompared(false);
    setBusy(false);
    setError(undefined);
  };

  const confirm = async () => {
    setBusy(true);
    setError(undefined);
    try {
      await withStepUp(() => pin({ variables: { id, fingerprint: cert.spkiSha256 } }));
      toast.success(WT.pin.pinned);
      reset();
      onOpenChange(false);
      onPinned();
    } catch (err) {
      if (!isStepUpCancelled(err)) {
        setError(hasApiCode(err, ApiCode.conflict) ? WT.pin.conflict : userMessage(err));
      }
      setBusy(false);
    }
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (busy) return;
        if (!o) reset();
        onOpenChange(o);
      }}
    >
      <DialogContent className="max-h-[calc(100dvh-2rem)] gap-5 overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{WT.pin.title}</DialogTitle>
          <DialogDescription>{WT.pin.lead}</DialogDescription>
        </DialogHeader>

        <section
          aria-label={WT.test.certificate}
          className="flex flex-col gap-4 rounded-md border border-primary/30 bg-accent/40 p-4"
        >
          <CertificateFacts cert={cert} plateTestId="pin-spki" />
          <CopyValue value={cert.spkiSha256} label={WT.cert.copySpki} />
          <div className="flex flex-col gap-1 text-xs text-muted-foreground">
            <p>{WT.pin.compareHint}</p>
            <code className="font-mono break-all text-foreground">{WT.pin.command}</code>
            <p>{WT.pin.browserHint}</p>
          </div>
        </section>

        <Alert>
          <AlertTriangle aria-hidden="true" />
          <AlertDescription>{WT.pin.renewal}</AlertDescription>
        </Alert>

        <label htmlFor={checkId} className="flex items-start gap-2 text-sm font-medium">
          <input
            id={checkId}
            type="checkbox"
            checked={compared}
            onChange={(e) => {
              setCompared(e.target.checked);
            }}
            className="mt-0.5 size-4 accent-primary"
          />
          {WT.pin.compared}
        </label>

        {error ? (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        ) : null}

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            disabled={busy}
            onClick={() => {
              reset();
              onOpenChange(false);
            }}
          >
            {WT.pin.cancel}
          </Button>
          <Button type="button" disabled={!compared || busy} onClick={() => void confirm()}>
            {busy ? (
              <Loader2 aria-hidden="true" className="animate-spin" />
            ) : (
              <ShieldCheck aria-hidden="true" />
            )}
            {WT.pin.confirm}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** The TLS state of a WebDAV destination (admins: settings are null for maintainers). */
export function WebdavTlsSection({
  id,
  trust,
  pinned,
  isAdmin,
}: {
  id: string;
  trust: WebdavTrust;
  pinned: WebdavCert | null | undefined;
  isAdmin: boolean;
}) {
  const { withStepUp } = useStepUp();
  const [clear] = useMutation(ClearWebdavCertificatePinMutation, {
    refetchQueries: [DestinationDetailQuery],
    awaitRefetchQueries: true,
  });
  const [confirming, setConfirming] = useState(false);
  return (
    <LedgerSection
      title={WT.tls.title}
      lead={WT.tls.lead}
      aside={<Tag tone={TRUST_TONE[trust]}>{TRUST_LABEL[trust]}</Tag>}
      actions={
        isAdmin && trust === "PINNED" ? (
          <Button
            type="button"
            size="sm"
            variant="outline"
            onClick={() => {
              setConfirming(true);
            }}
          >
            <ShieldOff aria-hidden="true" /> {WT.tls.removePin}
          </Button>
        ) : null
      }
    >
      <Facts>
        <Fact term={WT.tls.trust}>
          <span className="flex flex-col gap-0.5">
            <span>{TRUST_LABEL[trust]}</span>
            <span className="text-xs text-muted-foreground">{TRUST_DESCRIPTION[trust]}</span>
          </span>
        </Fact>
      </Facts>
      {trust === "PLAIN_HTTP" ? (
        <Alert variant="destructive">
          <AlertTriangle aria-hidden="true" />
          <AlertTitle>{TRUST_LABEL.PLAIN_HTTP}</AlertTitle>
          <AlertDescription>{TRUST_DESCRIPTION.PLAIN_HTTP}</AlertDescription>
        </Alert>
      ) : null}
      {pinned ? (
        <div className="flex flex-col gap-1.5" data-testid="pinned-certificate">
          <p className="eyebrow">{WT.tls.pinned}</p>
          <CertificateFacts cert={pinned} />
        </div>
      ) : trust !== "PLAIN_HTTP" ? (
        <p className="text-sm text-muted-foreground">{WT.tls.notPinnedHint}</p>
      ) : null}
      {isAdmin ? (
        <ConfirmDialog
          open={confirming}
          onOpenChange={setConfirming}
          title={WT.tls.removeTitle}
          description={WT.tls.removeLead}
          confirmLabel={WT.tls.removePin}
          destructive
          onConfirm={async () => {
            try {
              await withStepUp(() => clear({ variables: { id } }));
              toast.success(WT.tls.removed);
            } catch (err) {
              reportActionError(err);
              throw err;
            }
          }}
        />
      ) : null}
    </LedgerSection>
  );
}
