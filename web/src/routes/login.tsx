import { useApolloClient, useMutation } from "@apollo/client/react";
import { zodResolver } from "@hookform/resolvers/zod";
import { Loader2 } from "lucide-react";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { useLocation, useNavigate } from "react-router";
import { z } from "zod";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { AuthLayout } from "@/features/auth/auth-layout";
import { LoginMutation } from "@/features/auth/operations";
import { storeFactors } from "@/features/auth/pending-login";
import { ApiCode, firstApiError, userMessage } from "@/lib/api-error";
import { messages } from "@/lib/messages";
import { RETURN_PARAM, safeReturnPath, withReturnParam } from "@/lib/return-path";

const M = messages.login;

const loginSchema = z.object({
  email: z.string().trim().min(1, M.emailRequired).pipe(z.email(M.emailInvalid)),
  password: z.string().min(1, M.passwordRequired),
});
type LoginValues = z.infer<typeof loginSchema>;

/**
 * Sign-in, step 1: e-mail and password. Every failure reads the same
 * ("not correct"), whatever the cause (D-107); lockouts and rate limits show
 * the server's message with the wait time. There is no setup page: the
 * first admin is created with `bekup admin create` (D-113).
 */
export function LoginPage() {
  const navigate = useNavigate();
  const { search } = useLocation();
  const client = useApolloClient();
  const [login] = useMutation(LoginMutation);
  const [formError, setFormError] = useState<string>();
  const form = useForm<LoginValues>({
    resolver: zodResolver(loginSchema),
    defaultValues: { email: "", password: "" },
  });

  const onSubmit = async (values: LoginValues) => {
    setFormError(undefined);
    try {
      const { data } = await login({ variables: values });
      const result = data?.login;
      if (!result) throw new Error("empty login result");
      // A new session: nothing of a previous account may survive in the cache.
      await client.clearStore();
      if (result.secondFactorRequired) {
        storeFactors(result.availableFactors);
        await navigate(withReturnParam("/login/verify", search), { replace: true });
        return;
      }
      const next = safeReturnPath(new URLSearchParams(search).get(RETURN_PARAM)) ?? "/";
      await navigate(next, { replace: true });
    } catch (err) {
      const code = firstApiError(err)?.code;
      form.resetField("password");
      setFormError(code === ApiCode.unauthenticated ? M.invalidCredentials : userMessage(err));
      form.setFocus("password");
    }
  };

  const submitting = form.formState.isSubmitting;

  return (
    <AuthLayout eyebrow={M.eyebrow} title={M.title} step={0} footer={M.noRecoveryByMail}>
      <Form {...form}>
        <form
          onSubmit={(e) => void form.handleSubmit(onSubmit)(e)}
          className="flex flex-col gap-5"
          noValidate
        >
          {formError ? (
            <Alert variant="destructive">
              <AlertDescription>{formError}</AlertDescription>
            </Alert>
          ) : null}
          <FormField
            control={form.control}
            name="email"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{M.email}</FormLabel>
                <FormControl>
                  <Input
                    type="email"
                    autoComplete="username"
                    autoCapitalize="none"
                    spellCheck={false}
                    // eslint-disable-next-line jsx-a11y/no-autofocus -- the only task on this page
                    autoFocus
                    {...field}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name="password"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{M.password}</FormLabel>
                <FormControl>
                  <Input type="password" autoComplete="current-password" {...field} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <Button type="submit" className="mt-1 w-full" disabled={submitting}>
            {submitting ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
            {submitting ? M.submitting : M.submit}
          </Button>
        </form>
      </Form>
      <p className="border-t border-rule pt-4 text-xs text-muted-foreground">{M.passkeyNote}</p>
    </AuthLayout>
  );
}
