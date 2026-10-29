import { useMutation } from "@apollo/client/react";
import { zodResolver } from "@hookform/resolvers/zod";
import { Loader2, UserPlus } from "lucide-react";
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
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { reportActionError, useStepUp } from "@/features/auth/use-step-up";
import type { Role } from "@/graphql/gen/graphql";
import { ApiCode, applyFieldErrors, firstApiError, userMessage } from "@/lib/api-error";
import { messages } from "@/lib/messages";

import { usersMessages as M } from "./messages";
import { CreateUserMutation, UsersQuery } from "./operations";
import { RoleField } from "./role-field";
import type { IssuedPassword } from "./temporary-password-dialog";

const createUserSchema = z.object({
  email: z.string().trim().pipe(z.email(M.emailInvalid)),
  displayName: z.string().trim().min(1, M.nameRequired).max(200, M.nameTooLong),
  role: z.enum(["ADMIN", "MAINTAINER", "USER"]),
});
type Values = z.infer<typeof createUserSchema>;

/**
 * Creates an account (createUser, @stepUp). The server generates the
 * temporary password; it is handed to onCreated and shown once by the
 * caller, never written to the Apollo cache (fetchPolicy "no-cache").
 */
export function CreateUserDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: (issued: IssuedPassword) => void;
}) {
  const { withStepUp } = useStepUp();
  const [create] = useMutation(CreateUserMutation, {
    fetchPolicy: "no-cache",
    refetchQueries: [UsersQuery],
  });
  const form = useForm<Values>({
    resolver: zodResolver(createUserSchema),
    defaultValues: { email: "", displayName: "", role: "USER" },
  });

  const onSubmit = async (values: Values) => {
    try {
      const res = await withStepUp(() => create({ variables: { input: values } }));
      const out = res.data?.createUser;
      if (!out) throw new Error("empty createUser result");
      form.reset();
      onOpenChange(false);
      onCreated({
        email: out.user.email,
        password: out.temporaryPassword.password,
        expiresAt: out.temporaryPassword.expiresAt,
        kind: "created",
      });
    } catch (err) {
      if (firstApiError(err)?.code === ApiCode.conflict) {
        form.setError(
          "email",
          { type: "server", message: userMessage(err) },
          { shouldFocus: true },
        );
        return;
      }
      if (!applyFieldErrors(err, form.setError, ["email", "displayName", "role"])) {
        reportActionError(err);
      }
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
      <DialogContent className="gap-6 sm:max-w-xl">
        <DialogHeader className="gap-2">
          <span
            aria-hidden="true"
            className="inline-flex size-9 items-center justify-center rounded-md border border-primary/30 bg-accent text-primary"
          >
            <UserPlus className="size-4.5" />
          </span>
          <DialogTitle className="font-display text-2xl font-normal">{M.createTitle}</DialogTitle>
          <DialogDescription>{M.createLead}</DialogDescription>
        </DialogHeader>
        <Form {...form}>
          <form
            onSubmit={(e) => void form.handleSubmit(onSubmit)(e)}
            className="flex flex-col gap-5"
            noValidate
          >
            <div className="grid gap-4 sm:grid-cols-2">
              <FormField
                control={form.control}
                name="email"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{M.email}</FormLabel>
                    <FormControl>
                      <Input type="email" autoComplete="off" spellCheck={false} {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name="displayName"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{M.displayName}</FormLabel>
                    <FormControl>
                      <Input autoComplete="off" {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>
            <FormField
              control={form.control}
              name="role"
              render={({ field }) => (
                <FormItem>
                  <RoleField
                    name="create-role"
                    value={field.value}
                    onChange={(r: Role) => {
                      field.onChange(r);
                    }}
                  />
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
                {submitting ? M.creating : M.createSubmit}
              </Button>
            </DialogFooter>
          </form>
        </Form>
      </DialogContent>
    </Dialog>
  );
}
