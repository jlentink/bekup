import { useMutation } from "@apollo/client/react";
import { Copy, Download, FileDown, Loader2, TriangleAlert, Upload } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { isStepUpCancelled, useStepUp } from "@/features/auth/use-step-up";
import type {
  CreateDownloadTicketMutation as TicketResult,
  DownloadMode,
  JobEncryptionMode,
} from "@/graphql/gen/graphql";
import { extractIdentity } from "@/lib/age-identity";
import { userMessage } from "@/lib/api-error";
import { cn } from "@/lib/utils";

import { CreateDownloadTicketMutation } from "./operations";
import { restoreCommandFor } from "./download-model";

type Ticket = TicketResult["createDownloadTicket"];

export interface DownloadableArtifact {
  id: string;
  database?: string | null;
  kind: string;
  encryption: { mode?: JobEncryptionMode | null };
}

const T = {
  title: "Download backup",
  lead: (what: string) => `A single-use link for ${what}. Downloads are audited.`,
  mode: "What to download",
  raw: "Raw file",
  rawHint:
    "The stored object unchanged: zstd-compressed and, normally, age-encrypted. Verify or decrypt it offline with bekup decrypt or age.",
  plain: "Ready to use",
  plainHint: "Decrypted and decompressed on the server, ready for the restore command below.",
  identity: "age private key",
  identityHint:
    "This backup is encrypted to an age recipient. Paste the AGE-SECRET-KEY-1… line or choose the identity file. It is sent only with this request and kept sealed until the download starts.",
  identityFile: "Choose identity file",
  identityInvalid: "Enter one AGE-SECRET-KEY-1… identity.",
  create: "Create link",
  cancel: "Cancel",
  close: "Close",
  ready: "Your link is ready",
  readyLead: "It works once, in this browser session, until it expires.",
  expiresIn: (s: number) => `Expires in ${String(s)} s`,
  expired: "The link expired. Create a new one.",
  used: "Link used. Create a new one for another download.",
  get: (name: string) => `Download ${name}`,
  command: "Restore it with",
  commandHint: "Placeholders in angle brackets are yours to fill in.",
  copy: "Copy command",
  copied: "Command copied.",
  copyFailed: "Copying failed.",
  unencrypted: "This backup is not encrypted.",
} as const;

/**
 * The download dialog (D-150): admins and the job's owners only (the
 * caller hides it from maintainers). Choose raw or plain; plain downloads
 * of recipient-encrypted artifacts need the age identity, sent only in the
 * createDownloadTicket request. A fresh step-up is required; the ticket is
 * a single-use relative link that expires after 60 seconds and is never
 * cached (no-cache).
 */
export function DownloadDialog({
  artifact,
  open,
  onOpenChange,
}: {
  artifact: DownloadableArtifact;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { withStepUp } = useStepUp();
  const [create, { loading }] = useMutation(CreateDownloadTicketMutation, {
    fetchPolicy: "no-cache",
  });
  const [mode, setMode] = useState<DownloadMode>("PLAIN");
  const [identityText, setIdentityText] = useState("");
  const [ticket, setTicket] = useState<Ticket | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [used, setUsed] = useState(false);
  const [now, setNow] = useState(() => Date.now());
  const modeId = useId();
  const identityId = useId();
  const needsIdentity = mode === "PLAIN" && artifact.encryption.mode === "RECIPIENT";
  const identity = needsIdentity ? extractIdentity(identityText) : null;
  const identityError =
    needsIdentity && identityText.trim() !== "" && !identity ? T.identityInvalid : null;
  const what =
    artifact.database ?? (artifact.kind === "GLOBALS" ? "the cluster globals" : "this artifact");

  const expiresAt = ticket ? Date.parse(ticket.expiresAt) : 0;
  const remaining = Math.max(0, Math.ceil((expiresAt - now) / 1000));
  useEffect(() => {
    if (!ticket) return;
    const t = setInterval(() => {
      setNow(Date.now());
    }, 1000);
    return () => {
      clearInterval(t);
    };
  }, [ticket]);

  const reset = () => {
    setTicket(null);
    setIdentityText("");
    setError(null);
    setUsed(false);
  };
  const close = (o: boolean) => {
    if (loading) return;
    if (!o) reset();
    onOpenChange(o);
  };

  const submit = async () => {
    if (needsIdentity && !identity) return;
    setError(null);
    try {
      const r = await withStepUp(() =>
        create({
          variables: {
            artifactId: artifact.id,
            mode,
            ...(needsIdentity && identity ? { ageIdentity: identity } : {}),
          },
        }),
      );
      setIdentityText("");
      setNow(Date.now());
      setTicket(r.data?.createDownloadTicket ?? null);
    } catch (err) {
      if (!isStepUpCancelled(err)) setError(userMessage(err));
    }
  };

  const command = ticket
    ? restoreCommandFor(ticket.restoreCommand, ticket.fileName, artifact.database)
    : "";

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className="gap-5 sm:max-w-xl">
        <DialogHeader>
          <p className="eyebrow flex items-center gap-1.5">
            <FileDown aria-hidden="true" className="size-3.5" /> {T.title}
          </p>
          <DialogTitle className="font-display text-2xl font-normal">
            {ticket ? T.ready : what}
          </DialogTitle>
          <DialogDescription>{ticket ? T.readyLead : T.lead(what)}</DialogDescription>
        </DialogHeader>

        {!ticket ? (
          <form
            className="flex flex-col gap-5"
            noValidate
            onSubmit={(e) => {
              e.preventDefault();
              void submit();
            }}
          >
            <fieldset className="flex flex-col gap-2">
              <legend className="mb-2 text-sm font-medium">{T.mode}</legend>
              {(
                [
                  ["PLAIN", T.plain, T.plainHint],
                  ["RAW", T.raw, T.rawHint],
                ] as const
              ).map(([value, label, hint]) => (
                <label
                  key={value}
                  className={cn(
                    "grid cursor-pointer grid-cols-[auto_minmax(0,1fr)] items-start gap-x-3 gap-y-0.5 rounded-md border px-3.5 py-3",
                    "has-[:focus-visible]:ring-[3px] has-[:focus-visible]:ring-ring/50",
                    mode === value
                      ? "border-primary/60 bg-accent"
                      : "bg-surface hover:border-border-strong",
                  )}
                >
                  <input
                    type="radio"
                    name={modeId}
                    value={value}
                    checked={mode === value}
                    onChange={() => {
                      setMode(value);
                    }}
                    className="mt-0.5 size-4 accent-primary"
                  />
                  <span className="text-sm font-medium">{label}</span>
                  <span className="col-start-2 text-xs text-muted-foreground">{hint}</span>
                </label>
              ))}
            </fieldset>
            {artifact.encryption.mode === "NONE" ? (
              <p className="flex items-center gap-2 text-xs text-status-insecure-fg">
                <TriangleAlert aria-hidden="true" className="size-3.5" /> {T.unencrypted}
              </p>
            ) : null}
            {needsIdentity ? (
              <div className="flex flex-col gap-1.5">
                <label htmlFor={identityId} className="text-sm font-medium">
                  {T.identity}
                </label>
                <textarea
                  id={identityId}
                  rows={3}
                  spellCheck={false}
                  autoComplete="off"
                  aria-describedby={`${identityId}-hint`}
                  aria-invalid={Boolean(identityError)}
                  value={identityText}
                  onChange={(e) => {
                    setIdentityText(e.target.value);
                  }}
                  className="w-full rounded-md border border-input bg-surface px-3 py-2 font-mono text-xs shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 aria-invalid:border-destructive dark:bg-surface-sunken"
                />
                <p id={`${identityId}-hint`} className="text-xs text-muted-foreground">
                  {T.identityHint}
                </p>
                {identityError ? <p className="text-xs text-destructive">{identityError}</p> : null}
                <label className="inline-flex w-fit cursor-pointer items-center gap-1.5 rounded-md border px-2.5 py-1 text-xs hover:bg-accent has-[:focus-visible]:ring-[3px] has-[:focus-visible]:ring-ring/50">
                  <Upload aria-hidden="true" className="size-3.5" />
                  <span>Choose identity file</span>
                  <input
                    aria-label={T.identityFile}
                    type="file"
                    className="sr-only"
                    accept=".txt,.key,.age,text/plain"
                    onChange={(e) => {
                      const f = e.target.files?.[0];
                      e.target.value = "";
                      if (!f || f.size > 16 * 1024) return;
                      void f.text().then(setIdentityText);
                    }}
                  />
                </label>
              </div>
            ) : null}
            {error ? (
              <p role="alert" className="text-sm text-destructive">
                {error}
              </p>
            ) : null}
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                disabled={loading}
                onClick={() => {
                  close(false);
                }}
              >
                {T.cancel}
              </Button>
              <Button type="submit" disabled={loading || (needsIdentity && !identity)}>
                {loading ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
                {T.create}
              </Button>
            </DialogFooter>
          </form>
        ) : (
          <div className="flex flex-col gap-5">
            <div className="flex flex-wrap items-center gap-3">
              {remaining > 0 && !used ? (
                <Button asChild>
                  <a
                    href={ticket.url}
                    download={ticket.fileName}
                    rel="noopener noreferrer"
                    onClick={() => {
                      setUsed(true);
                    }}
                  >
                    <Download aria-hidden="true" /> {T.get(ticket.fileName)}
                  </a>
                </Button>
              ) : null}
              <span className="font-mono text-xs text-muted-foreground" aria-live="polite">
                {used ? T.used : remaining > 0 ? T.expiresIn(remaining) : T.expired}
              </span>
            </div>
            <div className="flex flex-col gap-1.5">
              <p className="eyebrow">{T.command}</p>
              <pre
                data-testid="restore-command"
                className="overflow-x-auto rounded-md border border-dashed border-border-strong/60 bg-surface-sunken px-4 py-3 font-mono text-xs whitespace-pre-wrap"
              >
                {command}
              </pre>
              <div className="flex items-center justify-between gap-3">
                <p className="text-xs text-muted-foreground">{T.commandHint}</p>
                <Button
                  variant="outline"
                  size="xs"
                  onClick={() => {
                    navigator.clipboard.writeText(command).then(
                      () => toast.success(T.copied),
                      () => toast.error(T.copyFailed),
                    );
                  }}
                >
                  <Copy aria-hidden="true" /> {T.copy}
                </Button>
              </div>
            </div>
            <DialogFooter>
              <Button
                variant="outline"
                onClick={() => {
                  close(false);
                }}
              >
                {T.close}
              </Button>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
