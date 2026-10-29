import { useApolloClient, useQuery } from "@apollo/client/react";
import { zodResolver } from "@hookform/resolvers/zod";
import {
  CheckCircle2,
  Clock3,
  Fingerprint,
  KeyRound,
  Loader2,
  PlugZap,
  ShieldCheck,
  Trash2,
  X,
} from "lucide-react";
import { useId, useState, type ReactNode } from "react";
import { useForm, useWatch } from "react-hook-form";
import { z } from "zod";

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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { getFragmentData } from "@/graphql/gen";
import type {
  CreateTemporaryConnectionInput,
  DatabaseEngine,
  RestoreFetchCertificateMutation as FetchCertificateResult,
  RestoreFetchSshHostKeyMutation as FetchHostKeyResult,
  RestoreTempConnectionFieldsFragment,
  RestoreTestConnectionMutation as TestResult,
} from "@/graphql/gen/graphql";
import { isStepUpCancelled, useStepUp } from "@/features/auth/use-step-up";
import { applyFieldErrors, userMessage } from "@/lib/api-error";
import { formatDateTime, formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

import { ENGINE_LABELS, PRIVILEGE_LABELS, privilegeReasonLabel } from "./model";
import {
  RestoreConfirmCertificatePinMutation,
  RestoreConfirmSshHostKeyMutation,
  RestoreCreateTemporaryConnectionMutation,
  RestoreDeleteTemporaryConnectionMutation,
  RestoreEngineVersionsQuery,
  RestoreFetchCertificateMutation,
  RestoreFetchSshHostKeyMutation,
  RestoreTempConnectionFieldsFragment as TempFields,
  RestoreTestConnectionMutation,
} from "./operations";

type TempConnection = RestoreTempConnectionFieldsFragment;

const port = z
  .string()
  .trim()
  .refine((v) => v === "" || (/^\d+$/.test(v) && Number(v) >= 1 && Number(v) <= 65535), {
    message: "Enter a port between 1 and 65535.",
  });

const schema = z
  .object({
    name: z.string().trim().max(200, "Use at most 200 characters."),
    versionKey: z.string().min(1, "Choose the server's version."),
    host: z.string().trim().min(1, "Enter the database host.").max(253, "The host is too long."),
    port,
    username: z.string().trim().min(1, "Enter the user name.").max(128, "The name is too long."),
    password: z.string().max(1024, "The password is too long."),
    tlsMode: z.enum(["PINNED", "UNVERIFIED", "DISABLED"]),
    sshEnabled: z.boolean(),
    sshHost: z.string().trim().max(253, "The host is too long."),
    sshPort: port,
    sshUser: z.string().trim().max(128, "The name is too long."),
    sshPassword: z.string().max(1024, "The password is too long."),
  })
  .superRefine((v, ctx) => {
    if (!v.sshEnabled) return;
    if (v.sshHost === "")
      ctx.addIssue({ code: "custom", path: ["sshHost"], message: "Enter the bastion host." });
    if (v.sshUser === "")
      ctx.addIssue({ code: "custom", path: ["sshUser"], message: "Enter the bastion user." });
  });
type Values = z.infer<typeof schema>;

const FIELDS = [
  "name",
  "versionKey",
  "host",
  "port",
  "username",
  "password",
  "tlsMode",
  "sshHost",
  "sshPort",
  "sshUser",
  "sshPassword",
] as const;

/** The CreateTemporaryConnectionInput of the form (secrets only when entered). */
function temporaryConnectionInput(
  v: Values,
  engine: DatabaseEngine,
): CreateTemporaryConnectionInput {
  return {
    engine,
    versionKey: v.versionKey,
    host: v.host.trim(),
    username: v.username.trim(),
    tlsMode: v.tlsMode,
    ...(v.name.trim() ? { name: v.name.trim() } : {}),
    ...(v.port.trim() ? { port: Number(v.port) } : {}),
    ...(v.password ? { password: v.password } : {}),
    ...(v.sshEnabled
      ? {
          ssh: {
            host: v.sshHost.trim(),
            user: v.sshUser.trim(),
            ...(v.sshPort.trim() ? { port: Number(v.sshPort) } : {}),
            ...(v.sshPassword ? { password: v.sshPassword } : {}),
          },
        }
      : {}),
  };
}

/**
 * Creates a temporary restore connection inside the wizard (task 4.4,
 * D-163; admins): the essentials of the connection editor, then the
 * certificate pin and the SSH host key, compared out of band before they
 * are confirmed. Nothing is trusted automatically.
 */
export function TemporaryConnectionPanel({
  engine,
  onClose,
  onReady,
}: {
  engine: string;
  onClose: () => void;
  onReady: (connectionId: string) => void;
}) {
  const [conn, setConn] = useState<TempConnection>();
  const headingId = useId();
  return (
    <section
      aria-labelledby={headingId}
      className="relative mt-2 flex flex-col gap-5 rounded-md border border-dashed border-border-strong/60 bg-surface-sunken/60 p-5"
    >
      <header className="flex items-start gap-3">
        <span
          aria-hidden="true"
          className="inline-flex size-8 items-center justify-center rounded-sm border bg-surface text-primary"
        >
          <PlugZap className="size-4" />
        </span>
        <div className="flex flex-col gap-0.5">
          <h3 id={headingId} className="font-semibold">
            Temporary restore connection
          </h3>
          <p className="text-sm text-muted-foreground">
            Usable only as a restore target. It and its stored credentials are deleted once the
            restores that used it have finished, or when it expires (the instance's
            restore.temp_connection_ttl).
          </p>
        </div>
        <Button
          variant="ghost"
          size="icon-sm"
          className="ml-auto"
          aria-label="Close the temporary connection form"
          onClick={onClose}
        >
          <X aria-hidden="true" />
        </Button>
      </header>
      {conn ? (
        <TemporaryConnectionSetup
          connection={conn}
          onChange={setConn}
          onDeleted={onClose}
          onReady={onReady}
        />
      ) : (
        <CreateForm engine={engine as DatabaseEngine} onCreated={setConn} />
      )}
    </section>
  );
}

function CreateForm({
  engine,
  onCreated,
}: {
  engine: DatabaseEngine;
  onCreated: (c: TempConnection) => void;
}) {
  const client = useApolloClient();
  const { withStepUp } = useStepUp();
  const [error, setError] = useState<string>();
  const versions = useQuery(RestoreEngineVersionsQuery, { variables: { engine } });
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: {
      name: "",
      versionKey: "",
      host: "",
      port: "",
      username: "",
      password: "",
      tlsMode: "PINNED",
      sshEnabled: false,
      sshHost: "",
      sshPort: "",
      sshUser: "",
      sshPassword: "",
    },
  });
  const [tlsMode, sshEnabled] = useWatch({
    control: form.control,
    name: ["tlsMode", "sshEnabled"],
  });

  const submit = form.handleSubmit(async (values) => {
    setError(undefined);
    try {
      const r = await withStepUp(() =>
        client.mutate({
          mutation: RestoreCreateTemporaryConnectionMutation,
          variables: { input: temporaryConnectionInput(values, engine) },
          fetchPolicy: "no-cache",
        }),
      );
      const created = r.data?.createTemporaryConnection;
      if (!created) throw new Error("no connection returned");
      // Drop the typed credentials from the form state at once.
      form.reset();
      onCreated(getFragmentData(TempFields, created));
    } catch (err) {
      if (isStepUpCancelled(err)) return;
      if (
        !applyFieldErrors(err, form.setError, FIELDS, {
          "ssh.host": "sshHost",
          "ssh.port": "sshPort",
          "ssh.user": "sshUser",
          "ssh.password": "sshPassword",
        })
      )
        setError(userMessage(err));
    }
  });

  return (
    <Form {...form}>
      <form
        noValidate
        className="grid gap-4 md:grid-cols-6"
        onSubmit={(e) => {
          void submit(e);
        }}
      >
        <FormField
          control={form.control}
          name="name"
          render={({ field }) => (
            <FormItem className="md:col-span-3">
              <FormLabel>Name (optional)</FormLabel>
              <FormControl>
                <Input {...field} autoComplete="off" placeholder="Temporary: <host>" />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name="versionKey"
          render={({ field }) => (
            <FormItem className="md:col-span-3">
              <FormLabel>{ENGINE_LABELS[engine] ?? engine} version</FormLabel>
              <Select value={field.value} onValueChange={field.onChange}>
                <FormControl>
                  <SelectTrigger className="w-full">
                    <SelectValue placeholder="Server release series" />
                  </SelectTrigger>
                </FormControl>
                <SelectContent>
                  {(versions.data?.engineVersions ?? []).map((v) => (
                    <SelectItem key={v.key} value={v.key}>
                      {v.key} (client {v.clientVersion}){v.legacy ? ", end of life" : ""}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name="host"
          render={({ field }) => (
            <FormItem className="md:col-span-4">
              <FormLabel>Host</FormLabel>
              <FormControl>
                <Input {...field} autoComplete="off" spellCheck={false} className="font-mono" />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name="port"
          render={({ field }) => (
            <FormItem className="md:col-span-2">
              <FormLabel>Port</FormLabel>
              <FormControl>
                <Input
                  {...field}
                  inputMode="numeric"
                  autoComplete="off"
                  placeholder={engine === "POSTGRES" ? "5432" : "3306"}
                />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name="username"
          render={({ field }) => (
            <FormItem className="md:col-span-3">
              <FormLabel>User</FormLabel>
              <FormControl>
                <Input {...field} autoComplete="off" spellCheck={false} />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name="password"
          render={({ field }) => (
            <FormItem className="md:col-span-3">
              <FormLabel>Password</FormLabel>
              <FormControl>
                <Input {...field} type="password" autoComplete="new-password" />
              </FormControl>
              <FormDescription>Write-only: stored encrypted, never shown again.</FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name="tlsMode"
          render={({ field }) => (
            <FormItem className="md:col-span-3">
              <FormLabel>Transport security</FormLabel>
              <Select
                value={field.value}
                onValueChange={(v) => {
                  field.onChange(v);
                }}
              >
                <FormControl>
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                </FormControl>
                <SelectContent>
                  <SelectItem value="PINNED">TLS, pinned certificate (recommended)</SelectItem>
                  <SelectItem value="UNVERIFIED">TLS without verification (insecure)</SelectItem>
                  <SelectItem value="DISABLED">No TLS (insecure)</SelectItem>
                </SelectContent>
              </Select>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name="sshEnabled"
          render={({ field }) => (
            <FormItem className="flex flex-row items-center gap-3 self-end md:col-span-3">
              <FormControl>
                <Switch checked={field.value} onCheckedChange={field.onChange} />
              </FormControl>
              <FormLabel>Reach it through an SSH bastion</FormLabel>
            </FormItem>
          )}
        />
        {tlsMode !== "PINNED" ? (
          <div className="hatch-insecure rounded-md border border-status-insecure/40 px-3 py-2 text-sm text-status-insecure-fg md:col-span-6">
            Without a pinned certificate the restored data travels without a verified server
            identity. This opt-out is audited and raises a security alert.
          </div>
        ) : null}
        {sshEnabled ? (
          <>
            <FormField
              control={form.control}
              name="sshHost"
              render={({ field }) => (
                <FormItem className="md:col-span-2">
                  <FormLabel>Bastion host</FormLabel>
                  <FormControl>
                    <Input {...field} autoComplete="off" spellCheck={false} className="font-mono" />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name="sshPort"
              render={({ field }) => (
                <FormItem className="md:col-span-1">
                  <FormLabel>Port</FormLabel>
                  <FormControl>
                    <Input {...field} inputMode="numeric" autoComplete="off" placeholder="22" />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name="sshUser"
              render={({ field }) => (
                <FormItem className="md:col-span-1">
                  <FormLabel>User</FormLabel>
                  <FormControl>
                    <Input {...field} autoComplete="off" spellCheck={false} />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name="sshPassword"
              render={({ field }) => (
                <FormItem className="md:col-span-2">
                  <FormLabel>Bastion password</FormLabel>
                  <FormControl>
                    <Input {...field} type="password" autoComplete="new-password" />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          </>
        ) : null}
        {error ? (
          <Alert variant="destructive" className="md:col-span-6">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        ) : null}
        <div className="md:col-span-6">
          <Button type="submit" disabled={form.formState.isSubmitting}>
            {form.formState.isSubmitting ? (
              <Loader2 aria-hidden="true" className="animate-spin" />
            ) : null}
            Create temporary connection
          </Button>
        </div>
      </form>
    </Form>
  );
}

/** Pins, host key and a test for a created temporary connection. */
function TemporaryConnectionSetup({
  connection: c,
  onChange,
  onDeleted,
  onReady,
}: {
  connection: TempConnection;
  onChange: (c: TempConnection) => void;
  onDeleted: () => void;
  onReady: (id: string) => void;
}) {
  const client = useApolloClient();
  const { withStepUp } = useStepUp();
  const [error, setError] = useState<string>();
  const [test, setTest] = useState<TestResult["testConnection"]>();
  const [busy, setBusy] = useState<string>();

  const run = async (label: string, op: () => Promise<void>) => {
    setError(undefined);
    setBusy(label);
    try {
      await op();
    } catch (err) {
      if (!isStepUpCancelled(err)) setError(userMessage(err));
    } finally {
      setBusy(undefined);
    }
  };

  const needsPin = c.tlsMode === "PINNED" && c.pinStatus === "MISSING";
  const needsHostKey = c.sshEnabled && !c.settings?.ssh?.hostKey?.confirmedAt;

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-x-6 gap-y-1 text-sm">
        <span className="font-medium">{c.name}</span>
        <span className="font-mono text-xs text-muted-foreground">
          {c.host}:{c.port}
        </span>
        {c.expiresAt ? (
          <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
            <Clock3 aria-hidden="true" className="size-3.5" />
            Expires {formatRelative(c.expiresAt)} (
            <time dateTime={c.expiresAt}>{formatDateTime(c.expiresAt)}</time>)
          </span>
        ) : null}
      </div>
      <ol className="flex flex-col divide-y divide-rule rounded-md border bg-surface">
        <SetupRow
          done={!needsPin}
          title={c.tlsMode === "PINNED" ? "Certificate pin" : "Certificate pin (not used)"}
          detail={
            c.tlsMode !== "PINNED"
              ? "Transport security is not pinned (insecure opt-out)."
              : c.settings?.tlsPin
                ? `Pinned: ${c.settings.tlsPin.subject}`
                : "Fetch the server's certificate and compare it out of band."
          }
        >
          {needsPin ? (
            <PinFlow
              busy={busy}
              run={run}
              fetchChain={async () => {
                const r = await client.mutate({
                  mutation: RestoreFetchCertificateMutation,
                  variables: { id: c.id },
                  fetchPolicy: "no-cache",
                });
                return r.data?.fetchCertificate;
              }}
              confirm={async (input) => {
                const r = await withStepUp(() =>
                  client.mutate({
                    mutation: RestoreConfirmCertificatePinMutation,
                    variables: { id: c.id, input },
                    fetchPolicy: "no-cache",
                  }),
                );
                const next = r.data?.confirmCertificatePin;
                if (next) onChange(getFragmentData(TempFields, next));
              }}
            />
          ) : null}
        </SetupRow>
        {c.sshEnabled ? (
          <SetupRow
            done={!needsHostKey}
            title="SSH host key"
            detail={
              c.settings?.ssh?.hostKey?.confirmedAt
                ? `Pinned: ${c.settings.ssh.hostKey.fingerprintSha256}`
                : "Fetch the bastion's host key and compare its fingerprint out of band."
            }
          >
            {needsHostKey ? (
              <HostKeyFlow
                busy={busy}
                run={run}
                fetchKey={async () => {
                  const r = await client.mutate({
                    mutation: RestoreFetchSshHostKeyMutation,
                    variables: { id: c.id },
                    fetchPolicy: "no-cache",
                  });
                  return r.data?.fetchSshHostKey;
                }}
                confirm={async (key) => {
                  const r = await withStepUp(() =>
                    client.mutate({
                      mutation: RestoreConfirmSshHostKeyMutation,
                      variables: {
                        id: c.id,
                        authorizedKey: key.authorizedKey,
                        expectedHost: key.host ?? "",
                        expectedPort: key.port ?? 22,
                      },
                      fetchPolicy: "no-cache",
                    }),
                  );
                  const next = r.data?.confirmSshHostKey;
                  if (next) onChange(getFragmentData(TempFields, next));
                }}
              />
            ) : null}
          </SetupRow>
        ) : null}
        <SetupRow
          done={test?.ok === true}
          title="Connection test"
          detail={
            test
              ? test.ok
                ? `Connected${test.serverVersion ? ` (server ${test.serverVersion})` : ""}.`
                : (test.message ?? "The test failed.")
              : "Optional: connect once with the stored credentials."
          }
        >
          <Button
            variant="outline"
            size="sm"
            disabled={busy !== undefined || needsPin || needsHostKey}
            onClick={() => {
              void run("test", async () => {
                const r = await client.mutate({
                  mutation: RestoreTestConnectionMutation,
                  variables: { id: c.id },
                  fetchPolicy: "no-cache",
                });
                setTest(r.data?.testConnection);
              });
            }}
          >
            {busy === "test" ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
            Test connection
          </Button>
          {test?.privileges?.acknowledgementRequired ? (
            <p className="text-xs text-destructive">
              Role verdict: {PRIVILEGE_LABELS[test.privileges.status]} (
              {test.privileges.reasons.map(privilegeReasonLabel).join("; ")}).
            </p>
          ) : null}
        </SetupRow>
      </ol>
      {error ? (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : null}
      <div className="flex flex-wrap gap-2">
        <Button
          disabled={!c.ready && (needsPin || needsHostKey)}
          onClick={() => {
            onReady(c.id);
          }}
        >
          <CheckCircle2 aria-hidden="true" />
          Use as restore target
        </Button>
        <Button
          variant="ghost"
          disabled={busy !== undefined}
          onClick={() => {
            void run("delete", async () => {
              await withStepUp(() =>
                client.mutate({
                  mutation: RestoreDeleteTemporaryConnectionMutation,
                  variables: { id: c.id },
                }),
              );
              onDeleted();
            });
          }}
        >
          <Trash2 aria-hidden="true" />
          Delete it
        </Button>
      </div>
    </div>
  );
}

function SetupRow({
  done,
  title,
  detail,
  children,
}: {
  done: boolean;
  title: string;
  detail: string;
  children?: ReactNode;
}) {
  return (
    <li className="flex flex-col gap-3 px-4 py-3">
      <div className="flex items-start gap-3">
        <span
          aria-hidden="true"
          className={cn(
            "mt-0.5 inline-flex size-5 shrink-0 items-center justify-center rounded-full border",
            done
              ? "border-status-success/50 bg-status-success-soft text-status-success-fg"
              : "border-border-strong/60 text-muted-foreground",
          )}
        >
          {done ? (
            <CheckCircle2 className="size-3.5" />
          ) : (
            <span className="size-1.5 rounded-full bg-current" />
          )}
        </span>
        <div className="flex min-w-0 flex-col">
          <span className="font-medium">
            {title}
            <span className="sr-only">{done ? " (done)" : " (to do)"}</span>
          </span>
          <span className="text-xs break-all text-muted-foreground">{detail}</span>
        </div>
      </div>
      {children ? <div className="flex flex-col gap-3 pl-8">{children}</div> : null}
    </li>
  );
}

type Chain = NonNullable<FetchCertificateResult["fetchCertificate"]>;
type Runner = (label: string, op: () => Promise<void>) => Promise<void>;

function PinFlow({
  busy,
  run,
  fetchChain,
  confirm,
}: {
  busy: string | undefined;
  run: Runner;
  fetchChain: () => Promise<Chain | undefined>;
  confirm: (input: {
    certIndex?: number;
    certificatePem?: string;
    leafSpkiSha256: string;
  }) => Promise<void>;
}) {
  const [chain, setChain] = useState<Chain>();
  const [anchor, setAnchor] = useState<number>();
  const [pem, setPem] = useState("");
  const [compared, setCompared] = useState(false);
  const pemId = useId();
  const comparedId = useId();

  if (!chain) {
    return (
      <div>
        <Button
          variant="outline"
          size="sm"
          disabled={busy !== undefined}
          onClick={() => {
            void run("fetch-cert", async () => {
              const c = await fetchChain();
              if (c) {
                setChain(c);
                setAnchor(c.suggestedAnchorIndex ?? undefined);
              }
            });
          }}
        >
          {busy === "fetch-cert" ? (
            <Loader2 aria-hidden="true" className="animate-spin" />
          ) : (
            <ShieldCheck aria-hidden="true" />
          )}
          Fetch certificate
        </Button>
      </div>
    );
  }
  const needsPem = chain.suggestedAnchorIndex == null;
  return (
    <div className="flex flex-col gap-3">
      <fieldset className="flex flex-col gap-2">
        <legend className="eyebrow mb-1">Presented chain (leaf first)</legend>
        {chain.certificates.map((cert, i) => (
          <label
            key={cert.fingerprintSha256}
            className={cn(
              "flex cursor-pointer gap-3 rounded-sm border bg-surface-sunken/50 p-3 text-xs",
              anchor === i && "border-primary",
            )}
          >
            <input
              type="radio"
              name="restore-pin-anchor"
              className="mt-0.5 size-4 accent-primary"
              checked={anchor === i}
              disabled={!(cert.selfSigned || cert.isCa)}
              onChange={() => {
                setAnchor(i);
              }}
            />
            <span className="grid min-w-0 gap-0.5 font-medium">
              {cert.subject}

              <span className="font-normal text-muted-foreground">issued by {cert.issuer}</span>
              <span className="text-muted-foreground">
                valid {formatDateTime(cert.notBefore)} to {formatDateTime(cert.notAfter)}
                {cert.isCa ? ", CA" : ""}
                {cert.selfSigned ? ", self-signed" : ""}
              </span>
              <span className="font-mono break-all">SHA-256 {cert.fingerprintSha256}</span>
              <span className="font-mono break-all text-muted-foreground">
                SPKI {cert.spkiSha256Hex}
              </span>
            </span>
          </label>
        ))}
      </fieldset>
      {needsPem ? (
        <div className="grid gap-1.5">
          <label htmlFor={pemId} className="text-sm font-medium">
            Root CA certificate (PEM), obtained out of band
          </label>
          <textarea
            id={pemId}
            value={pem}
            rows={4}
            spellCheck={false}
            className="rounded-md border border-input bg-surface p-2 font-mono text-xs"
            onChange={(e) => {
              setPem(e.target.value);
            }}
          />
        </div>
      ) : null}
      <label htmlFor={comparedId} className="flex items-start gap-2 text-sm">
        <input
          id={comparedId}
          type="checkbox"
          className="mt-0.5 size-4 accent-primary"
          checked={compared}
          onChange={(e) => {
            setCompared(e.target.checked);
          }}
        />
        I compared the fingerprint with the server's certificate through another channel.
      </label>
      <div>
        <Button
          size="sm"
          disabled={
            busy !== undefined || !compared || (needsPem ? pem.trim() === "" : anchor === undefined)
          }
          onClick={() => {
            void run("pin", () =>
              confirm(
                needsPem
                  ? { certificatePem: pem.trim(), leafSpkiSha256: chain.leafSpkiSha256 }
                  : { certIndex: anchor ?? 0, leafSpkiSha256: chain.leafSpkiSha256 },
              ),
            );
          }}
        >
          {busy === "pin" ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
          Pin this certificate
        </Button>
      </div>
    </div>
  );
}

type HostKey = NonNullable<FetchHostKeyResult["fetchSshHostKey"]>;

function HostKeyFlow({
  busy,
  run,
  fetchKey,
  confirm,
}: {
  busy: string | undefined;
  run: Runner;
  fetchKey: () => Promise<HostKey | undefined>;
  confirm: (key: HostKey) => Promise<void>;
}) {
  const [key, setKey] = useState<HostKey>();
  const [compared, setCompared] = useState(false);
  const comparedId = useId();
  if (!key) {
    return (
      <div>
        <Button
          variant="outline"
          size="sm"
          disabled={busy !== undefined}
          onClick={() => {
            void run("fetch-key", async () => {
              setKey(await fetchKey());
            });
          }}
        >
          {busy === "fetch-key" ? (
            <Loader2 aria-hidden="true" className="animate-spin" />
          ) : (
            <KeyRound aria-hidden="true" />
          )}
          Fetch host key
        </Button>
      </div>
    );
  }
  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-start gap-2 rounded-sm border bg-surface-sunken/50 p-3 text-xs">
        <Fingerprint aria-hidden="true" className="mt-0.5 size-4 text-muted-foreground" />
        <span className="grid gap-0.5">
          <span>
            {key.type} from{" "}
            <span className="font-mono">
              {key.host}:{key.port}
            </span>
          </span>
          <span className="font-mono break-all">{key.fingerprintSha256}</span>
        </span>
      </div>
      <label htmlFor={comparedId} className="flex items-start gap-2 text-sm">
        <input
          id={comparedId}
          type="checkbox"
          className="mt-0.5 size-4 accent-primary"
          checked={compared}
          onChange={(e) => {
            setCompared(e.target.checked);
          }}
        />
        I compared this fingerprint with the bastion's host key through another channel.
      </label>
      <div>
        <Button
          size="sm"
          disabled={busy !== undefined || !compared}
          onClick={() => {
            void run("host-key", () => confirm(key));
          }}
        >
          {busy === "host-key" ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
          Pin this host key
        </Button>
      </div>
    </div>
  );
}
