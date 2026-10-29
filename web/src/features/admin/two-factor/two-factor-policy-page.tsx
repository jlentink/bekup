import { useMutation, useQuery } from "@apollo/client/react";
import { zodResolver } from "@hookform/resolvers/zod";
import { ArrowLeft, Info, Loader2, ShieldAlert, ShieldCheck, Timer } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { useForm, useWatch } from "react-hook-form";
import { Link } from "react-router";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { PageHeader } from "@/components/page-header";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
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
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Section } from "@/features/account/section";
import { reportActionError, useStepUp } from "@/features/auth/use-step-up";
import type { AdminTwoFactorPolicyQuery, Role } from "@/graphql/gen/graphql";
import { applyFieldErrors, userMessage } from "@/lib/api-error";
import { formatDateTime, formatRelative } from "@/lib/format";
import { messages } from "@/lib/messages";
import { cn } from "@/lib/utils";

import { TwoFactorPolicyQuery, UpdateTwoFactorPolicyMutation } from "./operations";
import {
  newlyRequired,
  POLICY_ROLES,
  policyMessages as M,
  policySchema,
  samePolicy,
  toFormValues,
  toInput,
  weakenings,
  type PolicyFormValues,
  type Weakening,
} from "./policy";

type Policy = AdminTwoFactorPolicyQuery["twoFactorPolicy"];

/** The 2FA policy (admin, D-112): roles, grace period, step-up to save. */
export function TwoFactorPolicyPage() {
  const { data, error, refetch } = useQuery(TwoFactorPolicyQuery, {
    fetchPolicy: "cache-and-network",
  });
  return (
    <div className="flex flex-col gap-6">
      <Link
        to="/admin/users"
        className="inline-flex items-center gap-1.5 self-start text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft aria-hidden="true" className="size-3.5" /> {M.back}
      </Link>
      <PageHeader
        eyebrow={M.eyebrow}
        title={M.title}
        description={M.lead}
        actions={
          data ? (
            <span
              className="font-mono text-2xs text-muted-foreground"
              title={formatDateTime(data.twoFactorPolicy.updatedAt)}
            >
              {data.twoFactorPolicy.updatedAt
                ? M.lastChanged(formatRelative(data.twoFactorPolicy.updatedAt))
                : M.builtIn}
            </span>
          ) : null
        }
      />
      {error && !data ? (
        <Alert variant="destructive">
          <AlertDescription className="flex flex-wrap items-center gap-3">
            {userMessage(error)}
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                void refetch().catch(() => undefined);
              }}
            >
              {messages.common.retry}
            </Button>
          </AlertDescription>
        </Alert>
      ) : !data ? (
        <div className="flex flex-col gap-3" aria-busy="true">
          <Skeleton className="h-32 w-full" />
          <Skeleton className="h-20 w-full" />
        </div>
      ) : (
        <PolicyForm policy={data.twoFactorPolicy} />
      )}
    </div>
  );
}

function weakeningText(w: Weakening): string {
  switch (w.kind) {
    case "role":
      return M.weakenRole(messages.roles[w.role]);
    case "all":
      return M.weakenAll;
    case "grace":
      return M.weakenGrace(w.from, w.to);
  }
}

function PolicyForm({ policy }: { policy: Policy }) {
  const { withStepUp } = useStepUp();
  const [update] = useMutation(UpdateTwoFactorPolicyMutation, {
    update(cache, { data }) {
      if (data) {
        cache.writeQuery({
          query: TwoFactorPolicyQuery,
          data: { twoFactorPolicy: data.updateTwoFactorPolicy },
        });
      }
    },
  });
  const form = useForm<PolicyFormValues>({
    resolver: zodResolver(policySchema),
    defaultValues: toFormValues(policy),
  });
  const [confirming, setConfirming] = useState<PolicyFormValues | null>(null);
  const graceId = useId();

  // A saved policy (or a refetch) becomes the new baseline.
  useEffect(() => {
    form.reset(toFormValues(policy));
  }, [policy, form]);

  const watched = useWatch({ control: form.control });
  const requireForAll = watched.requireForAll ?? false;
  const roles: Role[] = watched.requiredRoles ?? [];
  const graceRaw = watched.gracePeriodDays ?? "";
  const graceOk = policySchema.shape.gracePeriodDays.safeParse(graceRaw).success;
  const draft = {
    requireForAll,
    requiredRoles: roles,
    gracePeriodDays: graceOk ? Number(graceRaw) : policy.gracePeriodDays,
  };
  const weak = weakenings(policy, draft);
  const added = newlyRequired(policy, draft);
  const changed = !graceOk || !samePolicy(policy, draft);

  const save = async (values: PolicyFormValues) => {
    try {
      await withStepUp(() => update({ variables: { input: toInput(values) } }));
      toast.success(M.saved);
    } catch (err) {
      if (!applyFieldErrors(err, form.setError, ["gracePeriodDays", "requiredRoles"])) {
        reportActionError(err);
      }
      throw err;
    }
  };

  const onSubmit = async (values: PolicyFormValues) => {
    const next = toInput(values);
    if (samePolicy(policy, next)) {
      toast.info(M.noChanges);
      return;
    }
    if (weakenings(policy, next).length > 0) {
      setConfirming(values);
      return;
    }
    await save(values).catch(() => undefined);
  };

  const submitting = form.formState.isSubmitting;

  return (
    <Form {...form}>
      <form
        noValidate
        onSubmit={(e) => void form.handleSubmit(onSubmit)(e)}
        className="flex max-w-5xl flex-col"
      >
        <Section icon={ShieldCheck} title={M.rolesHeading} lead={M.rolesLead}>
          <FormField
            control={form.control}
            name="requireForAll"
            render={({ field }) => (
              <FormItem className="flex flex-row items-center justify-between gap-4 rounded-md border bg-surface px-4 py-3">
                <div className="flex flex-col gap-0.5">
                  <FormLabel>{M.requireForAll}</FormLabel>
                  <FormDescription>{M.requireForAllHint}</FormDescription>
                </div>
                <FormControl>
                  <Switch checked={field.value} onCheckedChange={field.onChange} />
                </FormControl>
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name="requiredRoles"
            render={({ field }) => (
              <FormItem>
                <fieldset className="grid gap-2 sm:grid-cols-3">
                  <legend className="sr-only">{M.rolesHeading}</legend>
                  {POLICY_ROLES.map((role) => {
                    const on = requireForAll || field.value.includes(role);
                    return (
                      <label
                        key={role}
                        data-required={on}
                        className={cn(
                          "relative flex cursor-pointer flex-col gap-2 rounded-md border bg-surface p-3.5 transition-colors has-[:focus-visible]:ring-[3px] has-[:focus-visible]:ring-ring/50",
                          on ? "border-primary/50 bg-accent" : "hover:border-border-strong",
                          requireForAll && "cursor-not-allowed",
                        )}
                      >
                        <span className="flex items-center justify-between gap-2">
                          <span className="font-display text-xl">{messages.roles[role]}</span>
                          <input
                            type="checkbox"
                            className="size-4 accent-primary"
                            checked={on}
                            disabled={requireForAll}
                            onChange={(e) => {
                              field.onChange(
                                e.target.checked
                                  ? [...field.value, role]
                                  : field.value.filter((r) => r !== role),
                              );
                            }}
                            aria-describedby={`${graceId}-${role}`}
                          />
                        </span>
                        <span
                          className={cn("eyebrow", on ? "text-primary" : "text-muted-foreground")}
                        >
                          {on ? M.required : M.optional}
                        </span>
                        <span id={`${graceId}-${role}`} className="text-xs text-muted-foreground">
                          {M.roleHint[role]}
                        </span>
                      </label>
                    );
                  })}
                </fieldset>
                <FormMessage />
              </FormItem>
            )}
          />
          {!policy.passkeysAvailable ? (
            <p className="flex gap-2 text-xs text-muted-foreground">
              <Info aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" />
              {M.passkeysUnavailable}
            </p>
          ) : null}
        </Section>

        <Section icon={Timer} title={M.graceHeading} lead={M.graceLead}>
          <FormField
            control={form.control}
            name="gracePeriodDays"
            render={({ field }) => (
              <FormItem className="max-w-xs">
                <FormLabel>{M.graceLabel}</FormLabel>
                <div className="flex items-center gap-2">
                  <FormControl>
                    <Input
                      inputMode="numeric"
                      autoComplete="off"
                      className="w-24 font-mono"
                      {...field}
                    />
                  </FormControl>
                  <span className="text-sm text-muted-foreground">{M.graceUnit}</span>
                </div>
                <FormMessage />
              </FormItem>
            )}
          />
        </Section>

        <section
          aria-labelledby={`${graceId}-effects`}
          className="flex flex-col gap-4 border-t border-rule py-8"
        >
          <h2 id={`${graceId}-effects`} className="eyebrow">
            {M.effectsHeading}
          </h2>
          <ul className="flex flex-col gap-1.5 text-sm">
            {added.length > 0 ? (
              <li>{M.effectNewlyRequired(added.map((r) => messages.roles[r]).join(", "))}</li>
            ) : null}
            {added.length === 0 && weak.length === 0 ? <li>{M.effectNone}</li> : null}
            {graceOk ? <li>{M.effectGrace(Number(graceRaw))}</li> : null}
          </ul>
          {weak.length > 0 ? (
            <div
              role="alert"
              aria-labelledby={`${graceId}-weak`}
              className="hatch-insecure flex gap-3 rounded-md border border-status-insecure/50 px-4 py-3 text-status-insecure-fg"
            >
              <ShieldAlert aria-hidden="true" className="mt-0.5 size-5 shrink-0" />
              <div className="flex flex-col gap-1">
                <p id={`${graceId}-weak`} className="font-semibold">
                  {M.weakenTitle}
                </p>
                <ul className="list-disc pl-4 text-sm">
                  {weak.map((w) => (
                    <li key={`${w.kind}-${w.kind === "role" ? w.role : ""}`}>{weakeningText(w)}</li>
                  ))}
                </ul>
                <p className="text-sm">{M.weakenLead}</p>
              </div>
            </div>
          ) : null}
          <Button
            type="submit"
            className="self-start"
            variant={weak.length > 0 ? "destructive" : "default"}
            disabled={submitting || !changed}
          >
            {submitting ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
            {submitting ? M.saving : M.save}
          </Button>
        </section>
      </form>
      <ConfirmDialog
        open={confirming !== null}
        onOpenChange={(o) => {
          if (!o) setConfirming(null);
        }}
        title={M.weakenTitle}
        description={
          <>
            {weak.map(weakeningText).join(" ")} {M.weakenLead}
          </>
        }
        confirmLabel={M.confirmWeaken}
        destructive
        onConfirm={async () => {
          if (confirming) await save(confirming);
        }}
      />
    </Form>
  );
}
