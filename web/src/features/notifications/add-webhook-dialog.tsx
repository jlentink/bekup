import { useMutation, useQuery } from "@apollo/client/react";
import { zodResolver } from "@hookform/resolvers/zod";
import { Loader2, Webhook } from "lucide-react";
import { useForm } from "react-hook-form";
import { z } from "zod";

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
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { reportActionError, useStepUp } from "@/features/auth/use-step-up";
import { ApiCode, firstApiError, userMessage } from "@/lib/api-error";
import { messages } from "@/lib/messages";
import { ServerInfoQuery } from "@/lib/server-info";

import { notificationsMessages as M } from "./messages";
import { webhookUrlProblem } from "./model";
import { AddMyWebhookMutation } from "./operations";
import type { IssuedKey } from "./signing-key-dialog";

function schemaFor(allowHttp: boolean) {
  return z.object({
    label: z
      .string()
      .trim()
      .min(1, M.addWebhook.labelRequired)
      .max(100, M.addWebhook.labelRequired),
    url: z
      .string()
      .trim()
      .superRefine((v, ctx) => {
        const problem = webhookUrlProblem(v, allowHttp);
        if (problem) ctx.addIssue({ code: "custom", message: M.addWebhook.urlProblem[problem] });
      }),
  });
}
type Values = z.infer<ReturnType<typeof schemaFor>>;

/**
 * Adds a personal webhook (addMyWebhook, @stepUp). The URL gets early
 * feedback on the D-177 rules; the server's SSRF guard decides. The new
 * signing key goes to onAdded only (fetchPolicy "no-cache").
 */
export function AddWebhookDialog({
  open,
  onOpenChange,
  onAdded,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onAdded: (issued: IssuedKey) => void;
}) {
  const { withStepUp } = useStepUp();
  const { data: info } = useQuery(ServerInfoQuery, { fetchPolicy: "cache-first" });
  const allowHttp = info?.serverInfo.devMode === true;
  const [add] = useMutation(AddMyWebhookMutation, { fetchPolicy: "no-cache" });
  const form = useForm<Values>({
    resolver: zodResolver(schemaFor(allowHttp)),
    defaultValues: { label: "", url: "" },
    mode: "onTouched",
  });

  const onSubmit = async (values: Values) => {
    try {
      const res = await withStepUp(() =>
        add({ variables: { url: values.url, label: values.label } }),
      );
      const out = res.data?.addMyWebhook;
      if (!out) throw new Error("empty addMyWebhook result");
      form.reset();
      onOpenChange(false);
      onAdded({
        label: out.webhook.label,
        displayUrl: out.webhook.displayUrl,
        key: out.signingKey,
        kind: "added",
      });
    } catch (err) {
      const e = firstApiError(err);
      if (e?.code === ApiCode.validation) {
        const field = e.field === "label" ? "label" : "url";
        form.setError(field, { type: "server", message: userMessage(err) }, { shouldFocus: true });
        return;
      }
      reportActionError(err);
    }
  };

  const submitting = form.formState.isSubmitting;

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (submitting) return;
        if (!o) form.reset();
        onOpenChange(o);
      }}
    >
      <DialogContent className="gap-6 sm:max-w-lg">
        <DialogHeader className="gap-2">
          <span
            aria-hidden="true"
            className="inline-flex size-9 items-center justify-center rounded-md border border-primary/30 bg-accent text-primary"
          >
            <Webhook className="size-4.5" />
          </span>
          <DialogTitle className="font-display text-2xl font-normal">
            {M.addWebhook.title}
          </DialogTitle>
          <DialogDescription>{M.addWebhook.lead}</DialogDescription>
        </DialogHeader>
        <Form {...form}>
          <form
            onSubmit={(e) => void form.handleSubmit(onSubmit)(e)}
            className="flex flex-col gap-5"
            noValidate
          >
            <FormField
              control={form.control}
              name="label"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{M.addWebhook.label}</FormLabel>
                  <FormControl>
                    <Input autoComplete="off" maxLength={100} {...field} />
                  </FormControl>
                  <FormDescription>{M.addWebhook.labelHint}</FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name="url"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{M.addWebhook.url}</FormLabel>
                  <FormControl>
                    <Input
                      type="url"
                      inputMode="url"
                      autoComplete="off"
                      spellCheck={false}
                      placeholder="https://hooks.example.com/bekup"
                      className="font-mono text-[0.8125rem]"
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>{M.addWebhook.urlHint}</FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <DialogFooter>
              <DialogClose asChild>
                <Button type="button" variant="outline" disabled={submitting}>
                  {messages.common.cancel}
                </Button>
              </DialogClose>
              <Button type="submit" disabled={submitting}>
                {submitting ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
                {M.addWebhook.submit}
              </Button>
            </DialogFooter>
          </form>
        </Form>
      </DialogContent>
    </Dialog>
  );
}
