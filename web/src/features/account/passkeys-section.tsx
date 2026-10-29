import { useMutation } from "@apollo/client/react";
import { Fingerprint, Loader2, Pencil, Plus, Trash2, TriangleAlert } from "lucide-react";
import { useId, useState, type SyntheticEvent } from "react";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { reportActionError, useStepUp } from "@/features/auth/use-step-up";
import type { TwoFactorOverviewQuery as Overview } from "@/graphql/gen/graphql";
import { ApiCode, firstApiError, userMessage } from "@/lib/api-error";
import { formatDateTime, formatRelative } from "@/lib/format";
import { messages } from "@/lib/messages";
import { createCredential, WebAuthnError, webauthnSupported } from "@/lib/webauthn";

import {
  BeginPasskeyRegistrationMutation,
  DeletePasskeyMutation,
  FinishPasskeyRegistrationMutation,
  RenamePasskeyMutation,
  TwoFactorOverviewQuery,
} from "./operations";
import { Section, StatePill } from "./section";

const M = messages.passkeys;
const MAX_NAME = 64;

type Passkey = Overview["myPasskeys"][number];

/** Asks for a passkey name; submit throws to keep the dialog open with the error. */
function NameDialog({
  open,
  onOpenChange,
  title,
  initial,
  submitLabel,
  onSubmit,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  initial: string;
  submitLabel: string;
  onSubmit: (name: string) => Promise<string | undefined>;
}) {
  const [name, setName] = useState(initial);
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);
  const id = useId();
  const errorId = useId();
  const hintId = useId();

  const submit = async (e: SyntheticEvent) => {
    e.preventDefault();
    const v = name.trim();
    if (v === "" || Array.from(v).length > MAX_NAME) {
      setError(M.nameRequired);
      return;
    }
    setBusy(true);
    setError(undefined);
    const err = await onSubmit(v).finally(() => {
      setBusy(false);
    });
    if (err) setError(err);
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (busy) return;
        if (o) {
          setName(initial);
          setError(undefined);
        }
        onOpenChange(o);
      }}
    >
      <DialogContent className="sm:max-w-md">
        <form onSubmit={(e) => void submit(e)} noValidate className="grid gap-5">
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            <DialogDescription id={hintId}>{M.nameHint}</DialogDescription>
          </DialogHeader>
          <div className="grid gap-2">
            <Label htmlFor={id}>{M.nameLabel}</Label>
            <Input
              id={id}
              value={name}
              maxLength={MAX_NAME}
              autoComplete="off"
              disabled={busy}
              onChange={(e) => {
                setName(e.target.value);
              }}
              aria-invalid={error ? true : undefined}
              aria-describedby={error ? `${errorId} ${hintId}` : hintId}
            />
            {error ? (
              <p id={errorId} className="text-sm text-destructive">
                {error}
              </p>
            ) : null}
          </div>
          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" variant="outline" disabled={busy}>
                {messages.common.cancel}
              </Button>
            </DialogClose>
            <Button type="submit" disabled={busy}>
              {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
              {submitLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/** The message for a failed passkey action, or undefined when nothing should show (cancelled step-up). */
function passkeyFailure(err: unknown): string | undefined {
  if (err instanceof WebAuthnError) {
    return err.kind === "cancelled" ? M.cancelled : err.message;
  }
  const first = firstApiError(err);
  if (
    first?.code === ApiCode.validation ||
    first?.code === ApiCode.conflict ||
    first?.code === ApiCode.forbidden
  ) {
    return userMessage(err);
  }
  reportActionError(err);
  return undefined;
}

function PasskeyRow({ passkey }: { passkey: Passkey }) {
  const { withStepUp } = useStepUp();
  const [rename] = useMutation(RenamePasskeyMutation);
  const [remove] = useMutation(DeletePasskeyMutation, { refetchQueries: [TwoFactorOverviewQuery] });
  const [renaming, setRenaming] = useState(false);
  const [deleting, setDeleting] = useState(false);

  return (
    <li className="flex flex-col gap-2 py-3 first:pt-0 last:pb-0 sm:flex-row sm:items-center sm:gap-4">
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <p className="truncate font-medium">{passkey.name}</p>
        <p className="flex flex-wrap gap-x-3 gap-y-0.5 font-mono text-2xs text-muted-foreground">
          <span>{M.created(formatDateTime(passkey.createdAt))}</span>
          <span>
            {passkey.lastUsedAt ? M.lastUsed(formatRelative(passkey.lastUsedAt)) : M.neverUsed}
          </span>
          <span>{passkey.backupEligible ? M.synced : M.deviceBound}</span>
          {passkey.userVerified ? <span>{M.verified}</span> : null}
        </p>
        {passkey.signCountAnomalyAt ? (
          <p className="flex items-center gap-1.5 text-xs text-status-failed-fg">
            <TriangleAlert aria-hidden="true" className="size-3.5 shrink-0" />
            {M.anomaly}
          </p>
        ) : null}
      </div>
      <div className="flex gap-1">
        <Button
          variant="ghost"
          size="sm"
          onClick={() => {
            setRenaming(true);
          }}
          aria-label={`${M.rename}: ${passkey.name}`}
        >
          <Pencil aria-hidden="true" /> {M.rename}
        </Button>
        <Button
          variant="ghost"
          size="sm"
          className="text-destructive hover:text-destructive"
          onClick={() => {
            setDeleting(true);
          }}
          aria-label={`${M.delete}: ${passkey.name}`}
        >
          <Trash2 aria-hidden="true" /> {M.delete}
        </Button>
      </div>
      <NameDialog
        open={renaming}
        onOpenChange={setRenaming}
        title={M.rename}
        initial={passkey.name}
        submitLabel={messages.common.save}
        onSubmit={async (name) => {
          try {
            await withStepUp(() => rename({ variables: { id: passkey.id, name } }));
            toast.success(M.renamed);
            setRenaming(false);
            return undefined;
          } catch (err) {
            return passkeyFailure(err);
          }
        }}
      />
      <ConfirmDialog
        open={deleting}
        onOpenChange={setDeleting}
        title={M.deleteTitle(passkey.name)}
        description={M.deleteLead}
        confirmLabel={M.delete}
        destructive
        onConfirm={async () => {
          try {
            await withStepUp(() => remove({ variables: { id: passkey.id } }));
            toast.success(M.deleted);
          } catch (err) {
            const msg = passkeyFailure(err);
            if (msg) toast.error(msg);
            throw err;
          }
        }}
      />
    </li>
  );
}

export interface PasskeysSectionProps {
  passkeys: readonly Passkey[];
  available: boolean;
  reason: string | null | undefined;
  onAdded?: () => void;
}

/** Passkeys: list, rename, delete (step-up) and registration. */
export function PasskeysSection({ passkeys, available, reason, onAdded }: PasskeysSectionProps) {
  const { withStepUp } = useStepUp();
  const [begin] = useMutation(BeginPasskeyRegistrationMutation);
  const [finish] = useMutation(FinishPasskeyRegistrationMutation, {
    refetchQueries: [TwoFactorOverviewQuery],
    awaitRefetchQueries: true,
  });
  const [adding, setAdding] = useState(false);
  const browserOK = webauthnSupported();

  const register = async (name: string): Promise<string | undefined> => {
    try {
      const options = await withStepUp(() => begin());
      if (!options.data) throw new Error("no registration options");
      const response = await createCredential(options.data.beginPasskeyRegistration);
      await withStepUp(() => finish({ variables: { name, response } }));
      toast.success(M.added);
      setAdding(false);
      onAdded?.();
      return undefined;
    } catch (err) {
      return passkeyFailure(err);
    }
  };

  return (
    <Section
      icon={Fingerprint}
      title={M.heading}
      lead={M.lead}
      status={
        available ? (
          <StatePill on={passkeys.length > 0}>
            {passkeys.length > 0 ? M.count(passkeys.length) : M.empty}
          </StatePill>
        ) : null
      }
    >
      {!available ? (
        <Alert>
          <AlertDescription>
            <span className="font-medium text-foreground">{M.unavailable}</span> {reason ?? ""}
          </AlertDescription>
        </Alert>
      ) : (
        <>
          {passkeys.length > 0 ? (
            <ul className="divide-y divide-rule rounded-md border bg-surface px-4 py-3">
              {passkeys.map((p) => (
                <PasskeyRow key={p.id} passkey={p} />
              ))}
            </ul>
          ) : null}
          {browserOK ? (
            <Button
              variant="outline"
              size="sm"
              className="self-start"
              onClick={() => {
                setAdding(true);
              }}
            >
              <Plus aria-hidden="true" /> {M.add}
            </Button>
          ) : (
            <p className="text-sm text-muted-foreground">{M.unsupportedBrowser}</p>
          )}
          <NameDialog
            open={adding}
            onOpenChange={setAdding}
            title={M.add}
            initial=""
            submitLabel={messages.common.continue}
            onSubmit={register}
          />
        </>
      )}
    </Section>
  );
}
