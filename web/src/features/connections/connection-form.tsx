import { useMutation, useQuery } from "@apollo/client/react";
import { zodResolver } from "@hookform/resolvers/zod";
import { Loader2, ShieldOff } from "lucide-react";
import { useId, useMemo } from "react";
import { useForm, useWatch, type Resolver } from "react-hook-form";
import { Link, useNavigate } from "react-router";
import { toast } from "sonner";

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
import { NativeSelect } from "@/components/ui/native-select";
import { Switch } from "@/components/ui/switch";
import { reportActionError, useStepUp } from "@/features/auth/use-step-up";
import { applyFieldErrors } from "@/lib/api-error";
import { cn } from "@/lib/utils";

import {
  buildSchema,
  type ConnectionFormValues,
  effectiveSshAuth,
  emptyValues,
  FORM_FIELDS,
  type Original,
  retarget,
  SERVER_FIELDS,
  toCreateInput,
  toUpdateInput,
  valuesFrom,
} from "./form-model";
import { defaultPort, ENGINE_LABEL, ENGINES, TLS_MODE, TLS_MODES } from "./labels";
import {
  ConnectionDetailQuery,
  ConnectionEngineVersionsQuery,
  ConnectionsListQuery,
  CreateConnectionMutation,
  UpdateConnectionMutation,
} from "./operations";
import { FormGroup } from "./ui";
import { WriteOnlyField } from "./write-only-field";

const textarea =
  "w-full rounded-md border border-input bg-surface px-3 py-2 font-mono text-xs shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 aria-invalid:border-destructive dark:bg-surface-sunken";

/**
 * Create or edit a connection (admins; create/update need a step-up).
 * The engine and TLS mode are fixed after creation: the TLS mode changes
 * through its own audited flow on the detail page.
 */
export function ConnectionForm({ original }: { original?: Original }) {
  const editing = original !== undefined;
  const navigate = useNavigate();
  const { withStepUp } = useStepUp();
  const schema = useMemo(() => buildSchema(original), [original]);
  const form = useForm<ConnectionFormValues>({
    resolver: zodResolver(schema) as Resolver<ConnectionFormValues>,
    defaultValues: original ? valuesFrom(original) : emptyValues(),
  });
  const values = useWatch({ control: form.control }) as ConnectionFormValues;
  const engine = values.engine;
  const versions = useQuery(ConnectionEngineVersionsQuery, { variables: { engine } });
  const options = versions.data?.engineVersions ?? [];
  const moved = retarget(values, original);
  const s = original?.settings;
  const sshAuth = effectiveSshAuth(values, original);
  const sshStored = !!(original?.sshEnabled && s?.ssh && s.ssh.authMethod !== "NONE");

  const refetch = {
    refetchQueries: [ConnectionsListQuery, ConnectionDetailQuery],
    awaitRefetchQueries: true,
  };
  const [create] = useMutation(CreateConnectionMutation, {
    refetchQueries: [ConnectionsListQuery],
  });
  const [update] = useMutation(UpdateConnectionMutation, refetch);

  const onSubmit = async (v: ConnectionFormValues) => {
    try {
      if (original) {
        const input = toUpdateInput(v, original);
        if (Object.keys(input).length === 0) {
          toast.info("Nothing changed.");
          return;
        }
        await withStepUp(() => update({ variables: { id: original.id, input } }));
        toast.success("Connection saved.");
        void navigate(`/connections/${original.id}`);
      } else {
        const r = await withStepUp(() => create({ variables: { input: toCreateInput(v) } }));
        const id = r.data?.createConnection.id;
        toast.success(
          v.tlsMode === "PINNED"
            ? "Connection created. Next: pin the server's certificate."
            : "Connection created.",
        );
        void navigate(id ? `/connections/${id}` : "/connections");
      }
    } catch (err) {
      if (!applyFieldErrors(err, form.setError, FORM_FIELDS, SERVER_FIELDS)) {
        reportActionError(err);
      }
    }
  };

  const submitting = form.formState.isSubmitting;
  const cancelTo = original ? `/connections/${original.id}` : "/connections";

  return (
    <Form {...form}>
      <form
        onSubmit={(e) => void form.handleSubmit(onSubmit)(e)}
        noValidate
        className="flex flex-col"
        aria-label={editing ? "Edit connection" : "New connection"}
      >
        <FormGroup legend="Identity">
          <FormField
            control={form.control}
            name="name"
            render={({ field }) => (
              <FormItem>
                <FormLabel>Name</FormLabel>
                <FormControl>
                  <Input autoComplete="off" {...field} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
        </FormGroup>

        <FormGroup
          legend="Engine"
          lead="The version key selects the digest-pinned client image from the allowlisted catalog."
        >
          <FormField
            control={form.control}
            name="engine"
            render={({ field }) => (
              <FormItem>
                <FormLabel>Engine</FormLabel>
                <FormControl>
                  <NativeSelect
                    {...field}
                    disabled={editing}
                    onChange={(e) => {
                      field.onChange(e.target.value);
                      form.setValue("versionKey", "");
                      form.setValue("imageKey", "");
                    }}
                  >
                    {ENGINES.map((e) => (
                      <option key={e} value={e}>
                        {ENGINE_LABEL[e]}
                      </option>
                    ))}
                  </NativeSelect>
                </FormControl>
                {editing ? <FormDescription>The engine cannot change.</FormDescription> : null}
                <FormMessage />
              </FormItem>
            )}
          />
          <div className="grid gap-5 sm:grid-cols-2">
            <FormField
              control={form.control}
              name="versionKey"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>Server version</FormLabel>
                  <FormControl>
                    <NativeSelect {...field} disabled={versions.loading && options.length === 0}>
                      <option value="">Choose…</option>
                      {options.map((o) => (
                        <option key={o.key} value={o.key}>
                          {o.key} (client {o.clientVersion}){o.legacy ? " · legacy" : ""}
                        </option>
                      ))}
                    </NativeSelect>
                  </FormControl>
                  <FormDescription>
                    Not sure? Save, then use &ldquo;Detect version&rdquo; on the connection.
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name="imageKey"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>Client image</FormLabel>
                  <FormControl>
                    <NativeSelect {...field}>
                      <option value="">Same as the server version</option>
                      {options.map((o) => (
                        <option key={o.key} value={o.key}>
                          {o.key} (client {o.clientVersion})
                        </option>
                      ))}
                    </NativeSelect>
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>
          {options.find((o) => o.key === values.versionKey)?.legacy ? (
            <p className="text-sm text-status-partial-fg">
              This is an end-of-life series: it stays supported for backups but gets no new
              features.
            </p>
          ) : null}
        </FormGroup>

        <FormGroup
          legend="Endpoint"
          lead="Pinned TLS to an IP address or localhost needs an SSH tunnel (the certificate must name the host)."
        >
          <div className="grid gap-5 sm:grid-cols-[minmax(0,1fr)_8rem]">
            <FormField
              control={form.control}
              name="host"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>Host</FormLabel>
                  <FormControl>
                    <Input
                      autoComplete="off"
                      spellCheck={false}
                      placeholder="db.internal.example"
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
                  <FormLabel>Port</FormLabel>
                  <FormControl>
                    <Input
                      inputMode="numeric"
                      placeholder={String(defaultPort(engine))}
                      {...field}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>
        </FormGroup>

        <FormGroup
          legend="Database login"
          lead="Credentials are write-only: stored encrypted, never shown again."
        >
          <FormField
            control={form.control}
            name="username"
            render={({ field }) => (
              <FormItem>
                <FormLabel>User</FormLabel>
                <FormControl>
                  <Input autoComplete="off" spellCheck={false} {...field} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <WriteOnlyField
            label="Password"
            stored={s?.hasPassword === true}
            action={values.passwordAction}
            onAction={(a) => {
              form.setValue("passwordAction", a);
              form.setValue("password", "");
              form.clearErrors("password");
            }}
            allowClear
            reentry={
              moved.needsPassword
                ? "The database endpoint changed. Enter the password again or remove it: bekup never sends a stored password to a new endpoint."
                : undefined
            }
          >
            <FormField
              control={form.control}
              name="password"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{s?.hasPassword ? "New password" : "Password"}</FormLabel>
                  <FormControl>
                    <Input type="password" autoComplete="new-password" {...field} />
                  </FormControl>
                  {!editing ? (
                    <FormDescription>
                      Leave empty for roles that sign in without one.
                    </FormDescription>
                  ) : null}
                  <FormMessage />
                </FormItem>
              )}
            />
          </WriteOnlyField>
        </FormGroup>

        <FormGroup legend="Transport">
          {editing ? (
            <p className="text-sm">
              <span className="font-medium">{TLS_MODE[values.tlsMode].label}.</span>{" "}
              <span className="text-muted-foreground">
                Change the TLS mode and certificate pins on the connection page.
              </span>
            </p>
          ) : (
            <FormField
              control={form.control}
              name="tlsMode"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>TLS mode</FormLabel>
                  <FormControl>
                    <NativeSelect {...field}>
                      {TLS_MODES.map((m) => (
                        <option key={m} value={m}>
                          {TLS_MODE[m].label}
                          {m === "PINNED" ? " (recommended)" : ""}
                        </option>
                      ))}
                    </NativeSelect>
                  </FormControl>
                  <FormDescription>{TLS_MODE[values.tlsMode].description}</FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          )}
          {!editing && values.tlsMode !== "PINNED" ? (
            <Alert variant="destructive" className="hatch-insecure border-status-insecure/60">
              <ShieldOff aria-hidden="true" />
              <AlertTitle>Security opt-out</AlertTitle>
              <AlertDescription>
                Creating the connection without pinned TLS is audited, alerts every admin and shows
                a permanent &ldquo;Insecure transport&rdquo; badge.
              </AlertDescription>
            </Alert>
          ) : null}
          <ClientCertificateFields form={form} stored={!!s?.clientCertificatePem} />
        </FormGroup>

        <FormGroup
          legend="SSH tunnel"
          lead="Reach the database through a bastion. Its host key is confirmed on the connection page."
        >
          <FormField
            control={form.control}
            name="sshEnabled"
            render={({ field }) => (
              <FormItem className="flex flex-row items-center gap-3">
                <FormControl>
                  <Switch checked={field.value} onCheckedChange={field.onChange} />
                </FormControl>
                <FormLabel className="font-normal">Connect through an SSH bastion</FormLabel>
              </FormItem>
            )}
          />
          {editing && original.sshEnabled && !values.sshEnabled ? (
            <p className="text-sm text-status-partial-fg">
              Saving removes the tunnel with its stored credentials and pinned host key.
            </p>
          ) : null}
          {values.sshEnabled ? (
            <>
              <div className="grid gap-5 sm:grid-cols-[minmax(0,1fr)_7rem]">
                <FormField
                  control={form.control}
                  name="ssh.host"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Bastion host</FormLabel>
                      <FormControl>
                        <Input autoComplete="off" spellCheck={false} {...field} />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name="ssh.port"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Port</FormLabel>
                      <FormControl>
                        <Input inputMode="numeric" placeholder="22" {...field} />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>
              <FormField
                control={form.control}
                name="ssh.user"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>SSH user</FormLabel>
                    <FormControl>
                      <Input autoComplete="off" spellCheck={false} {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <SshAuthFields
                form={form}
                stored={sshStored}
                auth={sshAuth}
                reentry={moved.needsSshPassword}
              />
            </>
          ) : null}
        </FormGroup>

        <FormGroup
          legend="Run limit"
          lead="Backups and restores of this connection share the limit. Lowering it never stops active runs."
        >
          <FormField
            control={form.control}
            name="maxConcurrentRuns"
            render={({ field }) => (
              <FormItem className="max-w-56">
                <FormLabel>Concurrent runs</FormLabel>
                <FormControl>
                  <Input inputMode="numeric" placeholder="Global default" {...field} />
                </FormControl>
                <FormDescription>
                  1 to 64, capped by the instance-wide limit.
                  {original
                    ? ` Applies now: ${original.effectiveMaxConcurrentRuns}.`
                    : " Empty uses the global default."}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </FormGroup>

        <div className="sticky bottom-0 flex justify-end gap-2 border-t bg-background/95 py-4 backdrop-blur-sm">
          <Button asChild variant="outline">
            <Link to={cancelTo}>Cancel</Link>
          </Button>
          <Button type="submit" disabled={submitting}>
            {submitting ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
            {editing ? "Save changes" : "Create connection"}
          </Button>
        </div>
      </form>
    </Form>
  );
}

type FormApi = ReturnType<typeof useForm<ConnectionFormValues>>;

function ClientCertificateFields({ form, stored }: { form: FormApi; stored: boolean }) {
  const action = useWatch({ control: form.control, name: "clientCert" });
  const setAction = (a: ConnectionFormValues["clientCert"]) => {
    form.setValue("clientCert", a);
    form.setValue("clientCertificatePem", "");
    form.setValue("clientKeyPem", "");
  };
  if (action === "none" || action === "keep" || action === "clear") {
    return (
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <span className="text-muted-foreground">
          {action === "keep"
            ? "A TLS client certificate is stored."
            : action === "clear"
              ? "The client certificate is removed on save."
              : "No TLS client certificate."}
        </span>
        {action === "clear" ? (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => {
              setAction("keep");
            }}
          >
            Undo
          </Button>
        ) : (
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => {
              setAction("set");
            }}
          >
            {action === "keep" ? "Replace" : "Add a client certificate"}
          </Button>
        )}
        {action === "keep" ? (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => {
              setAction("clear");
            }}
          >
            Remove
          </Button>
        ) : null}
      </div>
    );
  }
  return (
    <div className="flex flex-col gap-4 rounded-md border border-dashed p-4">
      <FormField
        control={form.control}
        name="clientCertificatePem"
        render={({ field }) => (
          <FormItem>
            <FormLabel>Client certificate (PEM)</FormLabel>
            <FormControl>
              <textarea rows={4} spellCheck={false} className={textarea} {...field} />
            </FormControl>
            <FormMessage />
          </FormItem>
        )}
      />
      <FormField
        control={form.control}
        name="clientKeyPem"
        render={({ field }) => (
          <FormItem>
            <FormLabel>Client private key (PEM, write-only)</FormLabel>
            <FormControl>
              <textarea
                rows={4}
                spellCheck={false}
                autoComplete="off"
                className={cn(textarea, "[-webkit-text-security:disc]")}
                {...field}
              />
            </FormControl>
            <FormMessage />
          </FormItem>
        )}
      />
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className="self-start"
        onClick={() => {
          setAction(stored ? "keep" : "none");
        }}
      >
        Cancel
      </Button>
    </div>
  );
}

function SshAuthFields({
  form,
  stored,
  auth,
  reentry,
}: {
  form: FormApi;
  stored: boolean;
  auth: ConnectionFormValues["ssh"]["auth"];
  reentry: boolean;
}) {
  const group = useId();
  const choices: { value: ConnectionFormValues["ssh"]["auth"]; label: string }[] = [
    ...(stored && !reentry
      ? [{ value: "keep" as const, label: "Keep the stored credential" }]
      : []),
    ...(!stored
      ? [{ value: "later" as const, label: "Generate a keypair after saving (recommended)" }]
      : []),
    { value: "privateKey", label: "Paste a private key" },
    { value: "password", label: "A password" },
  ];
  return (
    <div className="flex flex-col gap-4">
      {reentry ? (
        <Alert>
          <AlertDescription>
            The bastion or SSH user changed. Enter the SSH password again or switch to a key: bekup
            never sends a stored password to a new endpoint.
          </AlertDescription>
        </Alert>
      ) : null}
      <fieldset className="flex flex-col gap-2">
        <legend className="mb-1 text-sm font-medium">SSH sign-in</legend>
        {choices.map((c) => (
          <label key={c.value} className="flex items-center gap-2 text-sm">
            <input
              type="radio"
              name={group}
              value={c.value}
              checked={auth === c.value}
              onChange={() => {
                form.setValue("ssh.auth", c.value);
                form.setValue("ssh.password", "");
                form.setValue("ssh.privateKey", "");
                form.setValue("ssh.privateKeyPassphrase", "");
              }}
              className="size-4 accent-primary"
            />
            {c.label}
          </label>
        ))}
      </fieldset>
      {auth === "password" ? (
        <FormField
          control={form.control}
          name="ssh.password"
          render={({ field }) => (
            <FormItem>
              <FormLabel>SSH password</FormLabel>
              <FormControl>
                <Input type="password" autoComplete="new-password" {...field} />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
      ) : null}
      {auth === "privateKey" ? (
        <>
          <FormField
            control={form.control}
            name="ssh.privateKey"
            render={({ field }) => (
              <FormItem>
                <FormLabel>Private key (write-only)</FormLabel>
                <FormControl>
                  <textarea
                    rows={5}
                    spellCheck={false}
                    autoComplete="off"
                    className={cn(textarea, "[-webkit-text-security:disc]")}
                    {...field}
                  />
                </FormControl>
                <FormDescription>
                  Prefer generating a keypair: the private key then never leaves the server.
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name="ssh.privateKeyPassphrase"
            render={({ field }) => (
              <FormItem>
                <FormLabel>Key passphrase (optional)</FormLabel>
                <FormControl>
                  <Input type="password" autoComplete="off" {...field} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
        </>
      ) : null}
    </div>
  );
}
