import { zodResolver } from "@hookform/resolvers/zod";
import { Fingerprint } from "lucide-react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import { z } from "zod";

import { Wordmark } from "@/components/brand/logo";
import { InsecureTransportBanner } from "@/components/shell/insecure-transport-banner";
import { ThemeToggle } from "@/components/theme-toggle";
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

const GUARANTEES = [
  ["Encrypted", "age, per-job keys, on by default"],
  ["Signed", "ed25519 manifests, verified before restore"],
  ["Isolated", "only the runner can reach Docker"],
  ["Guarded", "a fresh second factor for every restore"],
] as const;

const loginSchema = z.object({
  email: z
    .string()
    .trim()
    .min(1, "Enter your e-mail address.")
    .pipe(z.email("Enter a valid e-mail address.")),
  password: z.string().min(1, "Enter your password."),
});
type LoginValues = z.infer<typeof loginSchema>;

/**
 * Visual placeholder for the sign-in page. Task 6.1 adds the real flow
 * (password, passkey/TOTP, recovery codes, forced enrollment).
 */
export function LoginPage() {
  const form = useForm<LoginValues>({
    resolver: zodResolver(loginSchema),
    defaultValues: { email: "", password: "" },
  });

  const onSubmit = () => {
    toast.info("Sign-in is not wired up yet", { description: "It arrives in task 6.1." });
  };

  return (
    <div className="flex min-h-dvh flex-col">
      <InsecureTransportBanner />
      <div className="grid flex-1 lg:grid-cols-[minmax(0,1.1fr)_minmax(0,1fr)]">
        {/* The brand panel is always the dark "vault", in either theme. */}
        <section
          aria-label="About bekup"
          className="dark relative hidden flex-col justify-between overflow-hidden bg-background p-12 text-foreground lg:flex"
        >
          <div className="ledger-grid pointer-events-none absolute inset-0 [mask-image:radial-gradient(ellipse_at_30%_40%,black,transparent_75%)] opacity-70" />
          <Wordmark className="relative" />
          <div className="relative flex flex-col gap-10">
            <p className="font-display text-display-lg tracking-tight">
              Every backup,
              <br />
              <em className="text-primary">accounted for.</em>
            </p>
            <dl className="grid max-w-md grid-cols-[auto_1fr] border-t border-rule">
              {GUARANTEES.map(([term, detail]) => (
                <div
                  key={term}
                  className="col-span-2 grid grid-cols-subgrid border-b border-rule py-2.5"
                >
                  <dt className="eyebrow pr-6 text-foreground">{term}</dt>
                  <dd className="text-sm text-muted-foreground">{detail}</dd>
                </div>
              ))}
            </dl>
          </div>
          <p className="relative font-mono text-xs text-muted-foreground">
            v{__BEKUP_VERSION__} · self-hosted
          </p>
        </section>

        <section className="flex flex-col p-6 sm:p-10">
          <div className="flex items-center justify-between">
            <Wordmark className="lg:invisible" />
            <ThemeToggle />
          </div>
          <div className="mx-auto flex w-full max-w-sm flex-1 flex-col justify-center gap-8 py-12">
            <div className="flex flex-col gap-2">
              <p className="eyebrow">Sign in</p>
              <h1 className="font-display text-display leading-none">Welcome back</h1>
            </div>
            <Form {...form}>
              <form
                onSubmit={(e) => void form.handleSubmit(onSubmit)(e)}
                className="flex flex-col gap-5"
                noValidate
              >
                <FormField
                  control={form.control}
                  name="email"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>E-mail address</FormLabel>
                      <FormControl>
                        <Input
                          type="email"
                          autoComplete="username webauthn"
                          autoCapitalize="none"
                          spellCheck={false}
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
                      <FormLabel>Password</FormLabel>
                      <FormControl>
                        <Input type="password" autoComplete="current-password" {...field} />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <Button type="submit" className="mt-1 w-full">
                  Sign in
                </Button>
              </form>
            </Form>
            <div className="flex items-center gap-3 text-xs text-muted-foreground">
              <span className="h-px flex-1 bg-border" />
              or
              <span className="h-px flex-1 bg-border" />
            </div>
            <Button variant="outline" className="w-full" onClick={onSubmit}>
              <Fingerprint aria-hidden="true" /> Sign in with a passkey
            </Button>
          </div>
          <p className="mx-auto max-w-sm text-center text-xs text-muted-foreground">
            bekup will never ask for your recovery codes by email or chat.
          </p>
        </section>
      </div>
    </div>
  );
}
