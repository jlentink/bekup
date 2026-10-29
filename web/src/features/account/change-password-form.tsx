import { useMutation } from "@apollo/client/react";
import { zodResolver } from "@hookform/resolvers/zod";
import { Loader2 } from "lucide-react";
import { useDeferredValue, useMemo } from "react";
import { useForm, useWatch } from "react-hook-form";
import { toast } from "sonner";
import { z } from "zod";

import { Button } from "@/components/ui/button";
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
  useFormField,
} from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { MeQuery } from "@/features/auth/operations";
import { reportActionError, useStepUp } from "@/features/auth/use-step-up";
import { applyFieldErrors } from "@/lib/api-error";
import { messages } from "@/lib/messages";
import {
  estimateStrength,
  MAX_PASSWORD_LENGTH,
  MIN_PASSWORD_LENGTH,
} from "@/lib/password-strength";

import { ChangePasswordMutation } from "./operations";
import { StrengthMeter } from "./strength-meter";

type Estimate = ReturnType<typeof estimateStrength>;

/** The meter as the field's description (FormControl points aria-describedby at it). */
function FieldStrengthMeter({ estimate }: { estimate: Estimate | null }) {
  const { formDescriptionId } = useFormField();
  return <StrengthMeter estimate={estimate} id={formDescriptionId} />;
}

const M = messages.password;

const schema = z
  .object({
    currentPassword: z.string().min(1, M.currentRequired),
    newPassword: z
      .string()
      .min(MIN_PASSWORD_LENGTH, M.tooShort(MIN_PASSWORD_LENGTH))
      .max(MAX_PASSWORD_LENGTH, M.tooLong),
    confirmPassword: z.string(),
  })
  .refine((v) => v.newPassword === v.confirmPassword, {
    path: ["confirmPassword"],
    message: M.mismatch,
  });
type Values = z.infer<typeof schema>;

export interface ChangePasswordFormProps {
  /** The account's e-mail address and name (the policy refuses them in passwords). */
  account: readonly string[];
  /** Label of the current-password field (e.g. "Temporary password"). */
  currentLabel?: string;
  onChanged?: () => void;
}

/**
 * Changes the own password (changePassword, @stepUp). Field errors from the
 * server (wrong current password, policy refusal with its reason) land on
 * the fields; a missing step-up opens the dialog and retries.
 */
export function ChangePasswordForm({ account, currentLabel, onChanged }: ChangePasswordFormProps) {
  const { withStepUp } = useStepUp();
  const [change] = useMutation(ChangePasswordMutation, {
    refetchQueries: [MeQuery],
    awaitRefetchQueries: true,
  });
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { currentPassword: "", newPassword: "", confirmPassword: "" },
  });
  const pw = useDeferredValue(useWatch({ control: form.control, name: "newPassword" }));
  const estimate = useMemo(() => (pw ? estimateStrength(pw, account) : null), [pw, account]);

  const onSubmit = async (values: Values) => {
    try {
      await withStepUp(() =>
        change({
          variables: { currentPassword: values.currentPassword, newPassword: values.newPassword },
        }),
      );
      form.reset();
      toast.success(M.changed);
      onChanged?.();
    } catch (err) {
      if (!applyFieldErrors(err, form.setError, ["currentPassword", "newPassword"])) {
        reportActionError(err);
      }
    }
  };

  const submitting = form.formState.isSubmitting;

  return (
    <Form {...form}>
      <form
        onSubmit={(e) => void form.handleSubmit(onSubmit)(e)}
        className="flex flex-col gap-5"
        noValidate
      >
        {/* Lets password managers attach the new password to the account. */}
        <input
          type="text"
          name="username"
          autoComplete="username"
          value={account[0] ?? ""}
          readOnly
          hidden
        />
        <FormField
          control={form.control}
          name="currentPassword"
          render={({ field }) => (
            <FormItem>
              <FormLabel>{currentLabel ?? M.current}</FormLabel>
              <FormControl>
                <Input type="password" autoComplete="current-password" {...field} />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name="newPassword"
          render={({ field }) => (
            <FormItem>
              <FormLabel>{M.new}</FormLabel>
              <FormControl>
                <Input type="password" autoComplete="new-password" {...field} />
              </FormControl>
              <FieldStrengthMeter estimate={estimate} />
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name="confirmPassword"
          render={({ field }) => (
            <FormItem>
              <FormLabel>{M.confirm}</FormLabel>
              <FormControl>
                <Input type="password" autoComplete="new-password" {...field} />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
        <Button type="submit" className="self-start" disabled={submitting}>
          {submitting ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
          {submitting ? M.submitting : M.submit}
        </Button>
      </form>
    </Form>
  );
}
