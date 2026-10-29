import { useMutation, useQuery } from "@apollo/client/react";
import { zodResolver } from "@hookform/resolvers/zod";
import {
  CheckCircle2,
  Loader2,
  LockKeyhole,
  LockOpen,
  Mail,
  Send,
  ServerCog,
  ShieldCheck,
  TriangleAlert,
  XCircle,
} from "lucide-react";
import { useEffect, useId, useState } from "react";
import { useForm, useWatch } from "react-hook-form";
import { toast } from "sonner";

import { Tag } from "@/components/status/tag";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
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
import { useSessionUser } from "@/features/auth/use-session-user";
import { reportActionError, useStepUp } from "@/features/auth/use-step-up";
import { getFragmentData } from "@/graphql/gen";
import type { AdminSendTestEmailMutation, SmtpMode } from "@/graphql/gen/graphql";
import { applyFieldErrors, userMessage } from "@/lib/api-error";
import { formatDateTime, formatRelative } from "@/lib/format";
import { messages } from "@/lib/messages";
import { cn } from "@/lib/utils";

import {
  SendTestEmailMutation as SendTestEmail,
  SmtpSettingsQuery,
  UpdateSmtpSettingsMutation,
  SmtpSettingsFields,
} from "../operations";
import { docs } from "../docs";
import { DocLink, textareaClass } from "../ui";
import {
  mayBeUnencrypted,
  modeInfo,
  needsPasswordReentry,
  SMTP_MODES,
  smtpSchema,
  toFormValues,
  toInput,
  type SmtpFormValues,
  type SmtpShape,
} from "./model";

const T = {
  title: "E-mail (SMTP)",
  connection: "Relay",
  connectionLead:
    "bekup sends notifications and verification links through this relay. The host must pass the outbound SSRF guard: a relay on a private network needs an allowlist entry.",
  enabled: "Send e-mail",
  enabledHint: "Off: bekup sends no e-mail at all (the in-app inbox still works).",
  host: "Host",
  port: "Port",
  security: "Transport security",
  securityLead:
    "How the connection is protected. Prefer STARTTLS or implicit TLS: the other modes can expose mail and the relay password.",
  unencrypted: "E-mail may be sent unencrypted",
  unencryptedLead:
    "Mail, metadata and the SMTP password may travel in clear text. Saving this mode is written to the audit log.",
  auth: "Authentication",
  authLead: "Leave the username empty for a relay without AUTH. The password is write-only.",
  username: "Username",
  password: "Password",
  passwordStored: "Stored",
  passwordNever: "Write-only: never shown again.",
  passwordChange: "Change password",
  passwordRemove: "Remove",
  passwordKeep: "Keep the stored password",
  passwordUndo: "Undo",
  passwordRemoved: "Removed on save",
  reentry:
    "Host, port or username changed: enter the password again. bekup never sends a stored password to a new endpoint.",
  reentryMissing: "Enter the password for the new endpoint, or remove it.",
  sender: "Sender",
  fromAddress: "From address",
  fromName: "From name",
  fromNameHint: "Optional display name, e.g. “bekup backups”.",
  tls: "Certificates and timeouts",
  tlsLead: "A CA bundle replaces the system roots for this relay only (private CA).",
  caBundle: "CA bundle (PEM)",
  caBundleHint: "Empty: the system trust store.",
  connectTimeout: "Connect timeout (s)",
  sendTimeout: "Session timeout (s)",
  save: "Save settings",
  saved: "SMTP settings saved.",
  noChanges: "Nothing changed.",
  notConfigured: "Not configured",
  disabled: "E-mail off",
  active: "Sending",
  lastChanged: (when: string) => `Last changed ${when}`,
  test: "Test e-mail",
  testLead:
    "Sends a message with the saved settings (save first). The result shows whether the session was encrypted and with which TLS version.",
  testTo: "Recipient",
  testSend: "Send test e-mail",
  testOk: "Delivered to the relay",
  testFailed: "Not delivered",
  encrypted: "Encrypted",
  notEncrypted: "Not encrypted",
  noTls: "No TLS",
  guide: "SMTP guide",
} as const;

/** SMTP settings (admin, D-178/D-180): mode, write-only password, test e-mail. */
export function SmtpSettingsPage() {
  const { data, error, refetch } = useQuery(SmtpSettingsQuery, {
    fetchPolicy: "cache-and-network",
  });
  const settings = data ? getFragmentData(SmtpSettingsFields, data.smtpSettings) : undefined;
  return (
    <div className="flex flex-col">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-rule pb-4">
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="font-display text-3xl">{T.title}</h2>
          {settings ? <SmtpStatus settings={settings} /> : null}
        </div>
        <div className="flex items-center gap-3 text-xs text-muted-foreground">
          {settings?.updatedAt ? (
            <span className="font-mono text-2xs" title={formatDateTime(settings.updatedAt)}>
              {T.lastChanged(formatRelative(settings.updatedAt))}
            </span>
          ) : null}
          <DocLink href={docs.smtp}>{T.guide}</DocLink>
        </div>
      </div>
      {error && !settings ? (
        <Alert variant="destructive" className="mt-6">
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
      ) : !settings ? (
        <div className="mt-6 flex flex-col gap-3" aria-busy="true">
          <Skeleton className="h-40 w-full" />
          <Skeleton className="h-28 w-full" />
        </div>
      ) : (
        <>
          <SmtpForm settings={settings} />
          <TestEmailSection disabled={!settings.configured || !settings.enabled} />
        </>
      )}
    </div>
  );
}

function SmtpStatus({ settings }: { settings: SmtpShape & { mayBeUnencrypted: boolean } }) {
  return (
    <>
      {!settings.configured ? (
        <Tag tone="neutral">{T.notConfigured}</Tag>
      ) : settings.enabled ? (
        <Tag tone="success">{T.active}</Tag>
      ) : (
        <Tag tone="neutral">{T.disabled}</Tag>
      )}
      {settings.configured && settings.mayBeUnencrypted ? <UnencryptedBadge /> : null}
    </>
  );
}

/** The permanent "may be unencrypted" badge (hazard hatch, D-001). */
export function UnencryptedBadge() {
  return (
    <span
      data-testid="smtp-unencrypted-badge"
      className="hatch-insecure inline-flex h-6 items-center gap-1.5 rounded-sm border border-status-insecure/60 px-2 font-mono text-2xs font-semibold tracking-[0.08em] text-status-insecure-fg uppercase"
    >
      <LockOpen aria-hidden="true" className="size-3.5 shrink-0" />
      {T.unencrypted}
    </span>
  );
}

function SmtpForm({ settings }: { settings: SmtpShape }) {
  const { withStepUp } = useStepUp();
  const [update] = useMutation(UpdateSmtpSettingsMutation, {
    update(cache, { data }) {
      if (data) {
        cache.writeQuery({
          query: SmtpSettingsQuery,
          data: { smtpSettings: data.updateSmtpSettings },
        });
      }
    },
  });
  const form = useForm<SmtpFormValues>({
    resolver: zodResolver(smtpSchema),
    defaultValues: toFormValues(settings),
  });
  useEffect(() => {
    form.reset(toFormValues(settings));
  }, [settings, form]);

  const warnId = useId();
  const v = useWatch({ control: form.control }) as SmtpFormValues;
  const mode = v.mode;
  const reentry = needsPasswordReentry(settings, v);
  const pwAction = v.passwordAction;

  const onSubmit = async (values: SmtpFormValues) => {
    const forced = needsPasswordReentry(settings, values);
    if (forced && values.passwordAction !== "clear" && values.password === "") {
      form.setError(
        "password",
        { type: "manual", message: T.reentryMissing },
        { shouldFocus: true },
      );
      return;
    }
    const input = toInput({
      ...values,
      passwordAction: forced && values.passwordAction === "keep" ? "set" : values.passwordAction,
    });
    if (JSON.stringify(input) === JSON.stringify(toInput(toFormValues(settings)))) {
      toast.info(T.noChanges);
      return;
    }
    try {
      await withStepUp(() => update({ variables: { input } }));
      toast.success(T.saved);
    } catch (err) {
      if (
        !applyFieldErrors(err, form.setError, [
          "host",
          "port",
          "username",
          "password",
          "fromAddress",
          "fromName",
          "caBundlePem",
          "connectTimeoutSeconds",
          "sendTimeoutSeconds",
        ])
      ) {
        reportActionError(err);
      }
    }
  };

  const submitting = form.formState.isSubmitting;
  const showPasswordInput =
    !settings.hasPassword || pwAction === "set" || (reentry && pwAction !== "clear");

  return (
    <Form {...form}>
      <form
        noValidate
        onSubmit={(e) => void form.handleSubmit(onSubmit)(e)}
        className="flex flex-col"
      >
        <Section icon={ServerCog} title={T.connection} lead={T.connectionLead}>
          <FormField
            control={form.control}
            name="enabled"
            render={({ field }) => (
              <FormItem className="flex flex-row items-center justify-between gap-4 rounded-md border bg-surface px-4 py-3">
                <div className="flex flex-col gap-0.5">
                  <FormLabel>{T.enabled}</FormLabel>
                  <FormDescription>{T.enabledHint}</FormDescription>
                </div>
                <FormControl>
                  <Switch checked={field.value} onCheckedChange={field.onChange} />
                </FormControl>
              </FormItem>
            )}
          />
          <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_8rem]">
            <FormField
              control={form.control}
              name="host"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{T.host}</FormLabel>
                  <FormControl>
                    <Input
                      autoComplete="off"
                      spellCheck={false}
                      placeholder="smtp.example.com"
                      {...field}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name="port"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{T.port}</FormLabel>
                  <FormControl>
                    <Input inputMode="numeric" autoComplete="off" {...field} />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>
        </Section>

        <Section
          icon={mayBeUnencrypted(mode) ? LockOpen : LockKeyhole}
          title={T.security}
          lead={T.securityLead}
          status={mayBeUnencrypted(mode) ? <UnencryptedBadge /> : <Tag tone="success">TLS</Tag>}
        >
          <FormField
            control={form.control}
            name="mode"
            render={({ field }) => (
              <FormItem>
                <fieldset className="grid gap-2 sm:grid-cols-2">
                  <legend className="sr-only">{T.security}</legend>
                  {SMTP_MODES.map((m) => (
                    <ModeCard
                      key={m}
                      mode={m}
                      checked={field.value === m}
                      onSelect={() => {
                        field.onChange(m);
                      }}
                    />
                  ))}
                </fieldset>
              </FormItem>
            )}
          />
          {mayBeUnencrypted(mode) ? (
            <Alert className="border-status-insecure/50" aria-labelledby={warnId}>
              <TriangleAlert aria-hidden="true" />
              <AlertTitle id={warnId}>{T.unencrypted}</AlertTitle>
              <AlertDescription>{T.unencryptedLead}</AlertDescription>
            </Alert>
          ) : null}
        </Section>

        <Section icon={ShieldCheck} title={T.auth} lead={T.authLead}>
          <FormField
            control={form.control}
            name="username"
            render={({ field }) => (
              <FormItem className="max-w-md">
                <FormLabel>{T.username}</FormLabel>
                <FormControl>
                  <Input autoComplete="off" spellCheck={false} {...field} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          {settings.hasPassword && reentry && pwAction !== "clear" ? (
            <Alert>
              <LockKeyhole aria-hidden="true" />
              <AlertDescription>{T.reentry}</AlertDescription>
            </Alert>
          ) : null}
          {showPasswordInput ? (
            <div className="flex max-w-md flex-col gap-2">
              <FormField
                control={form.control}
                name="password"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{T.password}</FormLabel>
                    <FormControl>
                      <Input type="password" autoComplete="new-password" {...field} />
                    </FormControl>
                    <FormDescription>{T.passwordNever}</FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              {settings.hasPassword ? (
                <div className="flex gap-2">
                  {!reentry ? (
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      onClick={() => {
                        form.setValue("passwordAction", "keep", { shouldDirty: true });
                        form.setValue("password", "");
                      }}
                    >
                      {T.passwordKeep}
                    </Button>
                  ) : null}
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      form.setValue("passwordAction", "clear", { shouldDirty: true });
                      form.setValue("password", "");
                    }}
                  >
                    {T.passwordRemove}
                  </Button>
                </div>
              ) : null}
            </div>
          ) : (
            <div className="grid gap-2">
              <span className="text-sm font-medium">{T.password}</span>
              <div className="flex flex-wrap items-center gap-2">
                {pwAction === "clear" ? (
                  <Tag tone="warning">{T.passwordRemoved}</Tag>
                ) : (
                  <Tag tone="success">
                    <span className="sr-only">{T.password}: </span>
                    {T.passwordStored}
                  </Tag>
                )}
                {pwAction === "clear" ? (
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      form.setValue("passwordAction", "keep", { shouldDirty: true });
                    }}
                  >
                    {T.passwordUndo}
                  </Button>
                ) : (
                  <>
                    <span className="text-xs text-muted-foreground">{T.passwordNever}</span>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      onClick={() => {
                        form.setValue("passwordAction", "set", { shouldDirty: true });
                      }}
                    >
                      {T.passwordChange}
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      onClick={() => {
                        form.setValue("passwordAction", "clear", { shouldDirty: true });
                      }}
                    >
                      {T.passwordRemove}
                    </Button>
                  </>
                )}
              </div>
            </div>
          )}
        </Section>

        <Section icon={Mail} title={T.sender}>
          <div className="grid gap-4 sm:grid-cols-2">
            <FormField
              control={form.control}
              name="fromAddress"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{T.fromAddress}</FormLabel>
                  <FormControl>
                    <Input
                      type="email"
                      autoComplete="off"
                      placeholder="bekup@example.com"
                      {...field}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name="fromName"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{T.fromName}</FormLabel>
                  <FormControl>
                    <Input autoComplete="off" {...field} />
                  </FormControl>
                  <FormDescription>{T.fromNameHint}</FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>
        </Section>

        <Section icon={LockKeyhole} title={T.tls} lead={T.tlsLead}>
          <FormField
            control={form.control}
            name="caBundlePem"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{T.caBundle}</FormLabel>
                <FormControl>
                  <textarea
                    rows={4}
                    spellCheck={false}
                    placeholder="-----BEGIN CERTIFICATE-----"
                    className={textareaClass}
                    {...field}
                  />
                </FormControl>
                <FormDescription>{T.caBundleHint}</FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          <div className="grid max-w-md gap-4 sm:grid-cols-2">
            <FormField
              control={form.control}
              name="connectTimeoutSeconds"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{T.connectTimeout}</FormLabel>
                  <FormControl>
                    <Input inputMode="numeric" {...field} />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name="sendTimeoutSeconds"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{T.sendTimeout}</FormLabel>
                  <FormControl>
                    <Input inputMode="numeric" {...field} />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>
        </Section>

        <div className="flex items-center justify-end gap-3 border-b border-rule py-5">
          <Button type="submit" disabled={submitting}>
            {submitting ? (
              <Loader2 aria-hidden="true" className="animate-spin" />
            ) : (
              <ShieldCheck aria-hidden="true" />
            )}
            {T.save}
          </Button>
        </div>
      </form>
    </Form>
  );
}

function ModeCard({
  mode,
  checked,
  onSelect,
}: {
  mode: SmtpMode;
  checked: boolean;
  onSelect: () => void;
}) {
  const info = modeInfo[mode];
  const weak = mayBeUnencrypted(mode);
  const id = useId();
  return (
    <label
      htmlFor={id}
      data-checked={checked}
      className={cn(
        "relative flex cursor-pointer flex-col gap-1.5 rounded-md border bg-surface p-3.5 transition-colors has-[:focus-visible]:ring-[3px] has-[:focus-visible]:ring-ring/50",
        checked
          ? weak
            ? "border-status-insecure/60"
            : "border-primary/50 bg-accent"
          : "hover:border-border-strong",
      )}
    >
      <span className="flex items-center justify-between gap-2">
        <span className="flex items-center gap-2">
          <input
            id={id}
            aria-labelledby={`${id}-label`}
            aria-describedby={`${id}-lead`}
            type="radio"
            name="smtp-mode"
            className="size-4 accent-primary"
            checked={checked}
            onChange={onSelect}
          />
          <span id={`${id}-label`} className="font-medium">
            {info.label}
          </span>
        </span>
        <span className="font-mono text-2xs text-muted-foreground">:{info.port}</span>
      </span>
      <span id={`${id}-lead`} className="text-xs text-muted-foreground">
        {info.lead}
      </span>
      {weak ? (
        <span className="eyebrow inline-flex items-center gap-1 text-status-insecure-fg">
          <LockOpen aria-hidden="true" className="size-3" /> may be unencrypted
        </span>
      ) : null}
    </label>
  );
}

type TestResult = AdminSendTestEmailMutation["sendTestEmail"];

function TestEmailSection({ disabled }: { disabled: boolean }) {
  const user = useSessionUser();
  const [to, setTo] = useState(user.email);
  const [result, setResult] = useState<TestResult | null>(null);
  const [send, { loading }] = useMutation(SendTestEmail, { fetchPolicy: "no-cache" });
  const toId = useId();
  return (
    <Section icon={Send} title={T.test} lead={T.testLead}>
      <form
        className="flex max-w-xl flex-wrap items-end gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          setResult(null);
          send({ variables: { to: to.trim() } })
            .then((r) => {
              if (r.data) setResult(r.data.sendTestEmail);
            })
            .catch((err: unknown) => {
              reportActionError(err);
            });
        }}
      >
        <div className="flex min-w-60 flex-1 flex-col gap-2">
          <label htmlFor={toId} className="text-sm font-medium">
            {T.testTo}
          </label>
          <Input
            id={toId}
            type="email"
            value={to}
            onChange={(e) => {
              setTo(e.target.value);
            }}
          />
        </div>
        <Button type="submit" variant="outline" disabled={disabled || loading || to.trim() === ""}>
          {loading ? (
            <Loader2 aria-hidden="true" className="animate-spin" />
          ) : (
            <Send aria-hidden="true" />
          )}
          {T.testSend}
        </Button>
      </form>
      {result ? <TestResultCard result={result} /> : null}
    </Section>
  );
}

function TestResultCard({ result }: { result: TestResult }) {
  return (
    <div
      role="status"
      data-testid="smtp-test-result"
      className={cn(
        "grid max-w-xl gap-3 rounded-md border p-4",
        result.ok
          ? "border-status-success/40 bg-status-success-soft"
          : "border-status-failed/45 bg-status-failed-soft",
      )}
    >
      <p className="flex items-center gap-2 font-medium">
        {result.ok ? (
          <CheckCircle2 aria-hidden="true" className="size-4 text-status-success" />
        ) : (
          <XCircle aria-hidden="true" className="size-4 text-status-failed" />
        )}
        {result.ok ? T.testOk : T.testFailed}
        {result.code ? (
          <span className="font-mono text-2xs text-muted-foreground">{result.code}</span>
        ) : null}
      </p>
      {result.message ? <p className="text-sm">{result.message}</p> : null}
      {result.detail ? (
        <p className="font-mono text-xs text-muted-foreground">{result.detail}</p>
      ) : null}
      <div className="flex flex-wrap items-center gap-2">
        {result.encrypted ? (
          <Tag tone="success">{T.encrypted}</Tag>
        ) : (
          <Tag tone="insecure">{T.notEncrypted}</Tag>
        )}
        <span className="font-mono text-xs">{result.tlsVersion ?? T.noTls}</span>
      </div>
    </div>
  );
}
