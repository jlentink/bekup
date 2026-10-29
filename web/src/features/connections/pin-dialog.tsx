import { useMutation } from "@apollo/client/react";
import { Anchor, FileKey2, Info, Loader2, RefreshCw, ShieldCheck } from "lucide-react";
import { useId, useState } from "react";

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
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { isStepUpCancelled, useStepUp } from "@/features/auth/use-step-up";
import type { CertificatePinInput } from "@/graphql/gen/graphql";
import { apiErrors, ApiCode, userMessage } from "@/lib/api-error";
import { formatDateTime } from "@/lib/format";
import { cn } from "@/lib/utils";

import {
  ConfirmCertificatePinMutation,
  ConnectionDetailQuery,
  FetchCertificateMutation,
  StageBackupPinMutation,
} from "./operations";
import { canAnchor, chainRole, validity, type Chain, type ChainCert } from "./pin-model";

function ValidityPill({ cert }: { cert: ChainCert }) {
  switch (validity(cert)) {
    case "expired":
      return <Tag tone="danger">Expired</Tag>;
    case "not-yet":
      return <Tag tone="danger">Not yet valid</Tag>;
    case "7d":
      return <Tag tone="danger">Expires within 7 days</Tag>;
    case "30d":
      return <Tag tone="warning">Expires within 30 days</Tag>;
    case "ok":
      return <Tag tone="success">Valid</Tag>;
  }
}

function CertificateCard({
  cert,
  index,
  suggested,
  selectable,
  selected,
  onSelect,
  radioName,
}: {
  cert: ChainCert;
  index: number;
  suggested: boolean;
  selectable: boolean;
  selected: boolean;
  onSelect: () => void;
  radioName: string;
}) {
  const radioId = useId();
  const names = [...cert.dnsNames, ...cert.ipAddresses];
  return (
    <li
      data-testid={`chain-cert-${index}`}
      data-selected={selected}
      className={cn(
        "relative flex flex-col gap-3 rounded-md border bg-card p-4 transition-colors",
        selected && "border-primary ring-2 ring-primary/25",
      )}
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2.5">
          <span className="inline-flex size-6 items-center justify-center rounded-sm border bg-surface-sunken font-mono text-2xs">
            {index}
          </span>
          {selectable ? (
            <label htmlFor={radioId} className="flex items-center gap-2 font-medium">
              <input
                id={radioId}
                type="radio"
                name={radioName}
                checked={selected}
                onChange={onSelect}
                className="size-4 accent-primary"
              />
              {chainRole(cert, index)}
            </label>
          ) : (
            <span className="font-medium">{chainRole(cert, index)}</span>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-1.5">
          {suggested ? (
            <span className="inline-flex h-6 items-center gap-1 rounded-sm bg-primary px-2 text-xs font-medium text-primary-foreground">
              <Anchor aria-hidden="true" className="size-3" /> Suggested anchor
            </span>
          ) : null}
          <ValidityPill cert={cert} />
        </div>
      </div>
      <dl className="grid grid-cols-[6.5rem_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-sm">
        <dt className="eyebrow pt-0.5">Subject</dt>
        <dd className="font-mono text-xs break-all">{cert.subject}</dd>
        <dt className="eyebrow pt-0.5">Issuer</dt>
        <dd className="font-mono text-xs break-all">{cert.issuer}</dd>
        {names.length > 0 ? (
          <>
            <dt className="eyebrow pt-0.5">Names (SAN)</dt>
            <dd className="flex flex-wrap gap-1">
              {names.map((n) => (
                <span key={n} className="rounded-sm border px-1.5 font-mono text-2xs">
                  {n}
                </span>
              ))}
            </dd>
          </>
        ) : null}
        <dt className="eyebrow pt-0.5">Valid</dt>
        <dd className="text-xs">
          {formatDateTime(cert.notBefore)} → {formatDateTime(cert.notAfter)}
        </dd>
        <dt className="eyebrow pt-0.5">SPKI SHA-256</dt>
        <dd className="flex flex-col gap-0.5 font-mono text-2xs break-all">
          <span>{cert.spkiSha256}</span>
          <span className="text-muted-foreground">{cert.spkiSha256Hex}</span>
        </dd>
        <dt className="eyebrow pt-0.5">Cert SHA-256</dt>
        <dd className="font-mono text-2xs break-all text-muted-foreground">
          {cert.fingerprintSha256}
        </dd>
      </dl>
    </li>
  );
}

/** Why public CAs cannot be pinned (D-139). */
function PublicCaNote() {
  return (
    <Alert>
      <Info aria-hidden="true" />
      <AlertTitle>Why a public CA is refused</AlertTitle>
      <AlertDescription>
        <p>
          The database clients can only check a pinned CA, not the server&apos;s own key. A public
          CA issues certificates for any name to anyone who proves control of it, so pinning one
          would trust far more than your server. Pin a self-signed server certificate or your own
          private CA. A server with a publicly issued certificate can only use TLS without
          verification (an audited opt-out).
        </p>
      </AlertDescription>
    </Alert>
  );
}

export interface PinDialogProps {
  id: string;
  host: string;
  /** "pin": confirmCertificatePin; "stage": stageBackupPin. */
  mode: "pin" | "stage";
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

/**
 * The certificate pinning flow: capture the chain on the runner (nothing
 * trusted), show every certificate with its names, validity and SPKI
 * fingerprints, highlight the suggested anchor, have the admin compare the
 * leaf SPKI out of band, then confirm with a step-up. A root CA the server
 * does not send can be supplied as PEM instead.
 */
export function PinDialog({ id, host, mode, open, onOpenChange }: PinDialogProps) {
  const { withStepUp } = useStepUp();
  const refetch = { refetchQueries: [ConnectionDetailQuery], awaitRefetchQueries: true };
  const [fetchChain] = useMutation(FetchCertificateMutation);
  const [confirmPin] = useMutation(ConfirmCertificatePinMutation, refetch);
  const [stagePin] = useMutation(StageBackupPinMutation, refetch);

  const [tab, setTab] = useState<"capture" | "pem">("capture");
  const [chain, setChain] = useState<Chain>();
  const [anchor, setAnchor] = useState<number>();
  const [rotation, setRotation] = useState(false);
  const [compared, setCompared] = useState(false);
  const [pem, setPem] = useState("");
  const [spki, setSpki] = useState("");
  const [busy, setBusy] = useState<"fetch" | "confirm">();
  const [error, setError] = useState<string>();
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const radioName = useId();
  const comparedId = useId();
  const rotationId = useId();
  const pemId = useId();
  const spkiId = useId();

  const reset = () => {
    setChain(undefined);
    setAnchor(undefined);
    setRotation(false);
    setCompared(false);
    setPem("");
    setSpki("");
    setError(undefined);
    setFieldErrors({});
    setBusy(undefined);
    setTab("capture");
  };

  const capture = async () => {
    setBusy("fetch");
    setError(undefined);
    setFieldErrors({});
    setCompared(false);
    try {
      const r = await fetchChain({ variables: { id } });
      if (r.data) {
        const c = r.data.fetchCertificate;
        setChain(c);
        setAnchor(c.suggestedAnchorIndex ?? undefined);
        if (c.suggestedAnchorIndex == null) setTab("pem");
      }
    } catch (err) {
      setError(userMessage(err));
    } finally {
      setBusy(undefined);
    }
  };

  const anchorCert = chain && anchor !== undefined ? chain.certificates[anchor] : undefined;
  const anchorIsCa = tab === "capture" ? anchorCert?.isCa === true && anchor !== 0 : true;

  const submit = async () => {
    const input: CertificatePinInput =
      tab === "capture"
        ? {
            certIndex: anchor ?? null,
            ...(rotation && anchorIsCa
              ? { allowLeafRotation: true }
              : { leafSpkiSha256: chain?.leafSpkiSha256 ?? null }),
          }
        : {
            certificatePem: pem.trim(),
            ...(rotation ? { allowLeafRotation: true } : {}),
            ...(spki.trim() ? { leafSpkiSha256: spki.trim() } : {}),
          };
    setBusy("confirm");
    setError(undefined);
    setFieldErrors({});
    try {
      await withStepUp(async () => {
        if (mode === "pin") await confirmPin({ variables: { id, input } });
        else await stagePin({ variables: { id, input } });
      });
      reset();
      onOpenChange(false);
    } catch (err) {
      if (isStepUpCancelled(err)) {
        setBusy(undefined);
        return;
      }
      const fields: Record<string, string> = {};
      for (const e of apiErrors(err)) {
        if (e.code === ApiCode.validation && e.field) fields[e.field] = e.message;
      }
      setFieldErrors(fields);
      setError(userMessage(err));
      setBusy(undefined);
    }
  };

  const canSubmit =
    busy === undefined &&
    compared &&
    (tab === "capture"
      ? anchor !== undefined
      : pem.trim().startsWith("-----BEGIN CERTIFICATE-----") && (rotation || spki.trim() !== ""));

  const title = mode === "pin" ? "Pin the server certificate" : "Stage a backup pin";

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (busy) return;
        if (!o) reset();
        onOpenChange(o);
      }}
    >
      <DialogContent className="max-h-[calc(100dvh-2rem)] gap-5 overflow-y-auto sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>
            {mode === "pin"
              ? `The runner connects to ${host} without credentials and shows the chain it presents. Nothing is trusted until you compare the leaf's SPKI fingerprint with the server and confirm.`
              : "A backup pin is accepted alongside the current one during a planned certificate rotation. Confirm it as the pin once the server presents it."}
          </DialogDescription>
        </DialogHeader>

        <Tabs
          value={tab}
          onValueChange={(v) => {
            setTab(v === "pem" ? "pem" : "capture");
            setCompared(false);
          }}
        >
          <TabsList>
            <TabsTrigger value="capture">Capture from the server</TabsTrigger>
            <TabsTrigger value="pem">Paste a PEM</TabsTrigger>
          </TabsList>

          <TabsContent value="capture" className="flex flex-col gap-4 pt-4">
            {!chain ? (
              <Button
                type="button"
                variant="outline"
                className="self-start"
                disabled={busy === "fetch"}
                onClick={() => void capture()}
              >
                {busy === "fetch" ? (
                  <Loader2 aria-hidden="true" className="animate-spin" />
                ) : (
                  <FileKey2 aria-hidden="true" />
                )}
                Fetch certificate
              </Button>
            ) : (
              <>
                <div className="flex flex-col gap-1.5 rounded-md border border-primary/30 bg-accent/40 p-4">
                  <p className="eyebrow">Leaf SPKI SHA-256: compare this with the server</p>
                  <CopyValue value={chain.leafSpkiSha256} label="Copy the leaf SPKI fingerprint" />
                  <p className="text-xs text-muted-foreground">
                    On the server, for example:{" "}
                    <code className="font-mono">
                      openssl x509 -in server.crt -pubkey -noout | openssl pkey -pubin -outform der
                      | openssl dgst -sha256 -binary | base64
                    </code>
                  </p>
                </div>
                {chain.suggestedAnchorIndex == null ? (
                  <Alert>
                    <Info aria-hidden="true" />
                    <AlertDescription>
                      The server did not present a certificate that can be pinned (a self-signed
                      certificate or a CA). Paste your private root CA as PEM instead.
                    </AlertDescription>
                  </Alert>
                ) : null}
                <ol aria-label="Certificate chain, server first" className="flex flex-col gap-3">
                  {chain.certificates.map((cert, i) => (
                    <CertificateCard
                      key={cert.fingerprintSha256}
                      cert={cert}
                      index={i}
                      suggested={chain.suggestedAnchorIndex === i}
                      selectable={canAnchor(cert, i)}
                      selected={anchor === i}
                      onSelect={() => {
                        setAnchor(i);
                      }}
                      radioName={radioName}
                    />
                  ))}
                </ol>
                {fieldErrors["certIndex"] ? (
                  <p className="text-sm text-destructive">{fieldErrors["certIndex"]}</p>
                ) : null}
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="self-start"
                  disabled={busy !== undefined}
                  onClick={() => void capture()}
                >
                  <RefreshCw aria-hidden="true" /> Fetch again
                </Button>
              </>
            )}
          </TabsContent>

          <TabsContent value="pem" className="flex flex-col gap-4 pt-4">
            <div className="grid gap-2">
              <Label htmlFor={pemId}>Anchor certificate (PEM)</Label>
              <textarea
                id={pemId}
                value={pem}
                onChange={(e) => {
                  setPem(e.target.value);
                }}
                rows={7}
                spellCheck={false}
                placeholder="-----BEGIN CERTIFICATE-----"
                aria-invalid={fieldErrors["certificatePem"] ? true : undefined}
                className="w-full rounded-md border border-input bg-surface px-3 py-2 font-mono text-xs shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 aria-invalid:border-destructive dark:bg-surface-sunken"
              />
              {fieldErrors["certificatePem"] ? (
                <p className="text-sm text-destructive">{fieldErrors["certificatePem"]}</p>
              ) : (
                <p className="text-xs text-muted-foreground">
                  A self-signed server certificate or your private root CA, obtained out of band.
                </p>
              )}
            </div>
            <div className="grid gap-2">
              <Label htmlFor={spkiId}>Leaf SPKI SHA-256 (base64 or hex)</Label>
              <Input
                id={spkiId}
                value={spki}
                onChange={(e) => {
                  setSpki(e.target.value);
                }}
                className="font-mono text-xs"
                spellCheck={false}
                disabled={rotation}
                aria-invalid={fieldErrors["leafSpkiSha256"] ? true : undefined}
              />
              {fieldErrors["leafSpkiSha256"] ? (
                <p className="text-sm text-destructive">{fieldErrors["leafSpkiSha256"]}</p>
              ) : null}
            </div>
          </TabsContent>
        </Tabs>

        {(tab === "capture" && anchorIsCa && anchorCert) || tab === "pem" ? (
          <label htmlFor={rotationId} className="flex items-start gap-2 text-sm">
            <input
              id={rotationId}
              type="checkbox"
              checked={rotation}
              onChange={(e) => {
                setRotation(e.target.checked);
              }}
              className="mt-0.5 size-4 accent-primary"
            />
            <span>
              Allow leaf rotation (CA pins only): accept any certificate this CA issues for{" "}
              <span className="font-mono text-xs">{host}</span>, not just the current server key.
            </span>
          </label>
        ) : null}
        {fieldErrors["allowLeafRotation"] ? (
          <p className="text-sm text-destructive">{fieldErrors["allowLeafRotation"]}</p>
        ) : null}

        <PublicCaNote />

        {chain || tab === "pem" ? (
          <label htmlFor={comparedId} className="flex items-start gap-2 text-sm font-medium">
            <input
              id={comparedId}
              type="checkbox"
              checked={compared}
              onChange={(e) => {
                setCompared(e.target.checked);
              }}
              className="mt-0.5 size-4 accent-primary"
            />
            I compared the fingerprints with the server out of band and they match.
          </label>
        ) : null}

        {error ? (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        ) : null}

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            disabled={busy !== undefined}
            onClick={() => {
              reset();
              onOpenChange(false);
            }}
          >
            Cancel
          </Button>
          <Button type="button" disabled={!canSubmit} onClick={() => void submit()}>
            {busy === "confirm" ? (
              <Loader2 aria-hidden="true" className="animate-spin" />
            ) : (
              <ShieldCheck aria-hidden="true" />
            )}
            {mode === "pin" ? "Confirm pin" : "Stage backup pin"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
