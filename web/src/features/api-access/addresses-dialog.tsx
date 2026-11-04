import { useMutation } from "@apollo/client/react";
import { Loader2 } from "lucide-react";
import { useState, type SyntheticEvent } from "react";
import { toast } from "sonner";

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
import { isStepUpCancelled, useStepUp } from "@/features/auth/use-step-up";
import { firstApiError, userMessage } from "@/lib/api-error";
import { messages } from "@/lib/messages";

import { AddressEditor, type AddressDraft } from "./address-editor";
import { T } from "./messages";
import { addressProblem, splitAddresses } from "./model";
import { ApiTokensQuery, UpdateApiTokenAddressesMutation } from "./operations";

/**
 * Replaces a token's client addresses (updateApiTokenAddresses, step-up).
 * Mount it only while open (with a key per token) so it starts from the
 * stored values.
 */
export function AddressesDialog({
  token,
  onOpenChange,
}: {
  token: { id: string; name: string; allowedCidrs: readonly string[]; allowAnyAddress: boolean };
  onOpenChange: (open: boolean) => void;
}) {
  const { withStepUp } = useStepUp();
  const [update] = useMutation(UpdateApiTokenAddressesMutation, {
    refetchQueries: [ApiTokensQuery],
  });
  const [draft, setDraft] = useState<AddressDraft>({
    text: token.allowedCidrs.join("\n"),
    allowAny: token.allowAnyAddress,
  });
  const [fieldError, setFieldError] = useState<string>();
  const [formError, setFormError] = useState<string>();
  const [busy, setBusy] = useState(false);

  const submit = (e: SyntheticEvent) => {
    e.preventDefault();
    const list = splitAddresses(draft.text);
    const problem = draft.allowAny ? null : addressProblem(list);
    setFieldError(problem ?? undefined);
    setFormError(undefined);
    if (problem) return;
    setBusy(true);
    withStepUp(() =>
      update({
        variables: {
          id: token.id,
          allowedCidrs: draft.allowAny ? [] : list,
          allowAnyAddress: draft.allowAny,
        },
      }),
    )
      .then(() => {
        toast.success(T.edit.saved);
        onOpenChange(false);
      })
      .catch((err: unknown) => {
        if (isStepUpCancelled(err)) return;
        if (firstApiError(err)?.field === "allowedCidrs") setFieldError(userMessage(err));
        else setFormError(userMessage(err));
      })
      .finally(() => {
        setBusy(false);
      });
  };

  return (
    <Dialog
      open
      onOpenChange={(o) => {
        if (!busy) onOpenChange(o);
      }}
    >
      <DialogContent className="max-h-[90vh] gap-5 overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle className="font-display text-2xl font-normal">
            {T.edit.title(token.name)}
          </DialogTitle>
          <DialogDescription>{T.edit.lead}</DialogDescription>
        </DialogHeader>
        <form noValidate onSubmit={submit} className="flex flex-col gap-5">
          <AddressEditor value={draft} onChange={setDraft} error={fieldError} />
          {formError ? (
            <Alert variant="destructive">
              <AlertDescription>{formError}</AlertDescription>
            </Alert>
          ) : null}
          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" variant="outline" disabled={busy}>
                {messages.common.cancel}
              </Button>
            </DialogClose>
            <Button type="submit" disabled={busy}>
              {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
              {T.edit.submit}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
