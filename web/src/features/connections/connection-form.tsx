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
import { defineMessages } from "@/lib/i18n";
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

const T = defineMessages({
  en: {
    nothingChanged: "Nothing changed.",
    saved: "Connection saved.",
    createdPin: "Connection created. Next: pin the server's certificate.",
    created: "Connection created.",
    editForm: "Edit connection",
    newForm: "New connection",
    identity: "Identity",
    name: "Name",
    engine: "Engine",
    engineLead:
      "The version key selects the digest-pinned client image from the allowlisted catalog.",
    engineFixed: "The engine cannot change.",
    serverVersion: "Server version",
    choose: "Choose…",
    versionOption: (key: string, client: string, legacy: boolean) =>
      `${key} (client ${client})${legacy ? " · legacy" : ""}`,
    detectHint: "Not sure? Save, then use “Detect version” on the connection.",
    clientImage: "Client image",
    sameAsServer: "Same as the server version",
    imageOption: (key: string, client: string) => `${key} (client ${client})`,
    endOfLife:
      "This is an end-of-life series: it stays supported for backups but gets no new features.",
    endpoint: "Endpoint",
    endpointLead:
      "Pinned TLS to an IP address or localhost needs an SSH tunnel (the certificate must name the host).",
    host: "Host",
    port: "Port",
    login: "Database login",
    loginLead: "Credentials are write-only: stored encrypted, never shown again.",
    user: "User",
    password: "Password",
    passwordReentry:
      "The database endpoint changed. Enter the password again or remove it: bekup never sends a stored password to a new endpoint.",
    newPassword: "New password",
    passwordEmpty: "Leave empty for roles that sign in without one.",
    transport: "Transport",
    changeOnPage: "Change the TLS mode and certificate pins on the connection page.",
    tlsMode: "TLS mode",
    recommended: " (recommended)",
    optOut: "Security opt-out",
    optOutBody:
      "Creating the connection without pinned TLS is audited, alerts every admin and shows a permanent “Insecure transport” badge.",
    sshTunnel: "SSH tunnel",
    sshLead:
      "Reach the database through a bastion. Its host key is confirmed on the connection page.",
    sshSwitch: "Connect through an SSH bastion",
    sshRemoved: "Saving removes the tunnel with its stored credentials and pinned host key.",
    bastionHost: "Bastion host",
    sshUser: "SSH user",
    runLimit: "Run limit",
    runLimitLead:
      "Backups and restores of this connection share the limit. Lowering it never stops active runs.",
    concurrentRuns: "Concurrent runs",
    globalDefault: "Global default",
    runsHint: (effective: number | undefined) =>
      `1 to 64, capped by the instance-wide limit.${
        effective !== undefined ? ` Applies now: ${effective}.` : " Empty uses the global default."
      }`,
    cancel: "Cancel",
    saveChanges: "Save changes",
    create: "Create connection",
    clientCertStored: "A TLS client certificate is stored.",
    clientCertRemoved: "The client certificate is removed on save.",
    noClientCert: "No TLS client certificate.",
    undo: "Undo",
    replace: "Replace",
    addClientCert: "Add a client certificate",
    remove: "Remove",
    clientCertPem: "Client certificate (PEM)",
    clientKeyPem: "Client private key (PEM, write-only)",
    keepCredential: "Keep the stored credential",
    generateLater: "Generate a keypair after saving (recommended)",
    pasteKey: "Paste a private key",
    aPassword: "A password",
    sshReentry:
      "The bastion or SSH user changed. Enter the SSH password again or switch to a key: bekup never sends a stored password to a new endpoint.",
    sshSignIn: "SSH sign-in",
    sshPassword: "SSH password",
    privateKey: "Private key (write-only)",
    preferGenerate: "Prefer generating a keypair: the private key then never leaves the server.",
    passphrase: "Key passphrase (optional)",
  },
  nl: {
    nothingChanged: "Er is niets gewijzigd.",
    saved: "Verbinding opgeslagen.",
    createdPin: "Verbinding aangemaakt. Volgende stap: pin het certificaat van de server.",
    created: "Verbinding aangemaakt.",
    editForm: "Verbinding bewerken",
    newForm: "Nieuwe verbinding",
    identity: "Identiteit",
    name: "Naam",
    engine: "Engine",
    engineLead:
      "De versiesleutel kiest het client-image, vastgepind op digest, uit de toegestane catalogus.",
    engineFixed: "De engine kan niet worden gewijzigd.",
    serverVersion: "Serverversie",
    choose: "Kies…",
    versionOption: (key: string, client: string, legacy: boolean) =>
      `${key} (client ${client})${legacy ? " · verouderd" : ""}`,
    detectHint:
      "Weet je het niet zeker? Sla op en gebruik daarna „Versie detecteren” bij de verbinding.",
    clientImage: "Client-image",
    sameAsServer: "Gelijk aan de serverversie",
    imageOption: (key: string, client: string) => `${key} (client ${client})`,
    endOfLife:
      "Dit is een reeks aan het einde van haar levensduur: back-ups blijven ondersteund, maar er komen geen nieuwe functies.",
    endpoint: "Eindpunt",
    endpointLead:
      "Vastgepinde TLS naar een IP-adres of localhost vereist een SSH-tunnel (het certificaat moet de host noemen).",
    host: "Host",
    port: "Poort",
    login: "Database-login",
    loginLead: "Inloggegevens zijn alleen-schrijven: versleuteld opgeslagen en nooit meer getoond.",
    user: "Gebruiker",
    password: "Wachtwoord",
    passwordReentry:
      "Het eindpunt van de database is gewijzigd. Vul het wachtwoord opnieuw in of verwijder het: bekup stuurt een opgeslagen wachtwoord nooit naar een nieuw eindpunt.",
    newPassword: "Nieuw wachtwoord",
    passwordEmpty: "Laat leeg voor rollen die zonder wachtwoord inloggen.",
    transport: "Transport",
    changeOnPage: "Wijzig de TLS-modus en certificaatpins op de pagina van de verbinding.",
    tlsMode: "TLS-modus",
    recommended: " (aanbevolen)",
    optOut: "Afwijking van de veilige standaard",
    optOutBody:
      "Een verbinding zonder vastgepinde TLS aanmaken wordt vastgelegd in de auditlog, waarschuwt elke beheerder en toont een permanent label „Onveilig transport”.",
    sshTunnel: "SSH-tunnel",
    sshLead:
      "Bereik de database via een bastionhost. De hostsleutel bevestig je op de pagina van de verbinding.",
    sshSwitch: "Verbinden via een SSH-bastionhost",
    sshRemoved:
      "Opslaan verwijdert de tunnel met de opgeslagen inloggegevens en de vastgepinde hostsleutel.",
    bastionHost: "Bastionhost",
    sshUser: "SSH-gebruiker",
    runLimit: "Runlimiet",
    runLimitLead:
      "Back-ups en herstel van deze verbinding delen de limiet. Verlagen stopt nooit actieve runs.",
    concurrentRuns: "Gelijktijdige runs",
    globalDefault: "Globale standaard",
    runsHint: (effective: number | undefined) =>
      `1 tot en met 64, begrensd door de limiet van de instantie.${
        effective !== undefined
          ? ` Geldt nu: ${effective}.`
          : " Leeg gebruikt de globale standaard."
      }`,
    cancel: "Annuleren",
    saveChanges: "Wijzigingen opslaan",
    create: "Verbinding aanmaken",
    clientCertStored: "Er is een TLS-clientcertificaat opgeslagen.",
    clientCertRemoved: "Het clientcertificaat wordt verwijderd bij opslaan.",
    noClientCert: "Geen TLS-clientcertificaat.",
    undo: "Ongedaan maken",
    replace: "Vervangen",
    addClientCert: "Een clientcertificaat toevoegen",
    remove: "Verwijderen",
    clientCertPem: "Clientcertificaat (PEM)",
    clientKeyPem: "Privésleutel van de client (PEM, alleen-schrijven)",
    keepCredential: "Opgeslagen inloggegevens behouden",
    generateLater: "Na opslaan een sleutelpaar genereren (aanbevolen)",
    pasteKey: "Een privésleutel plakken",
    aPassword: "Een wachtwoord",
    sshReentry:
      "De bastionhost of SSH-gebruiker is gewijzigd. Vul het SSH-wachtwoord opnieuw in of stap over op een sleutel: bekup stuurt een opgeslagen wachtwoord nooit naar een nieuw eindpunt.",
    sshSignIn: "SSH-login",
    sshPassword: "SSH-wachtwoord",
    privateKey: "Privésleutel (alleen-schrijven)",
    preferGenerate: "Genereer liever een sleutelpaar: de privésleutel verlaat de server dan nooit.",
    passphrase: "Wachtwoordzin van de sleutel (optioneel)",
  },
});

/** Example host name (technical, not translated). */
const HOST_EXAMPLE = "db.internal.example";

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
          toast.info(T.nothingChanged);
          return;
        }
        await withStepUp(() => update({ variables: { id: original.id, input } }));
        toast.success(T.saved);
        void navigate(`/connections/${original.id}`);
      } else {
        const r = await withStepUp(() => create({ variables: { input: toCreateInput(v) } }));
        const id = r.data?.createConnection.id;
        toast.success(v.tlsMode === "PINNED" ? T.createdPin : T.created);
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
        aria-label={editing ? T.editForm : T.newForm}
      >
        <FormGroup legend={T.identity}>
          <FormField
            control={form.control}
            name="name"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{T.name}</FormLabel>
                <FormControl>
                  <Input autoComplete="off" {...field} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
        </FormGroup>

        <FormGroup legend={T.engine} lead={T.engineLead}>
          <FormField
            control={form.control}
            name="engine"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{T.engine}</FormLabel>
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
                {editing ? <FormDescription>{T.engineFixed}</FormDescription> : null}
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
                  <FormLabel>{T.serverVersion}</FormLabel>
                  <FormControl>
                    <NativeSelect {...field} disabled={versions.loading && options.length === 0}>
                      <option value="">{T.choose}</option>
                      {options.map((o) => (
                        <option key={o.key} value={o.key}>
                          {T.versionOption(o.key, o.clientVersion, o.legacy)}
                        </option>
                      ))}
                    </NativeSelect>
                  </FormControl>
                  <FormDescription>{T.detectHint}</FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name="imageKey"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{T.clientImage}</FormLabel>
                  <FormControl>
                    <NativeSelect {...field}>
                      <option value="">{T.sameAsServer}</option>
                      {options.map((o) => (
                        <option key={o.key} value={o.key}>
                          {T.imageOption(o.key, o.clientVersion)}
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
            <p className="text-sm text-status-partial-fg">{T.endOfLife}</p>
          ) : null}
        </FormGroup>

        <FormGroup legend={T.endpoint} lead={T.endpointLead}>
          <div className="grid gap-5 sm:grid-cols-[minmax(0,1fr)_8rem]">
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
                      placeholder={HOST_EXAMPLE}
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

        <FormGroup legend={T.login} lead={T.loginLead}>
          <FormField
            control={form.control}
            name="username"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{T.user}</FormLabel>
                <FormControl>
                  <Input autoComplete="off" spellCheck={false} {...field} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <WriteOnlyField
            label={T.password}
            stored={s?.hasPassword === true}
            action={values.passwordAction}
            onAction={(a) => {
              form.setValue("passwordAction", a);
              form.setValue("password", "");
              form.clearErrors("password");
            }}
            allowClear
            reentry={moved.needsPassword ? T.passwordReentry : undefined}
          >
            <FormField
              control={form.control}
              name="password"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{s?.hasPassword ? T.newPassword : T.password}</FormLabel>
                  <FormControl>
                    <Input type="password" autoComplete="new-password" {...field} />
                  </FormControl>
                  {!editing ? <FormDescription>{T.passwordEmpty}</FormDescription> : null}
                  <FormMessage />
                </FormItem>
              )}
            />
          </WriteOnlyField>
        </FormGroup>

        <FormGroup legend={T.transport}>
          {editing ? (
            <p className="text-sm">
              <span className="font-medium">{TLS_MODE[values.tlsMode].label}.</span>{" "}
              <span className="text-muted-foreground">{T.changeOnPage}</span>
            </p>
          ) : (
            <FormField
              control={form.control}
              name="tlsMode"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{T.tlsMode}</FormLabel>
                  <FormControl>
                    <NativeSelect {...field}>
                      {TLS_MODES.map((m) => (
                        <option key={m} value={m}>
                          {TLS_MODE[m].label}
                          {m === "PINNED" ? T.recommended : ""}
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
              <AlertTitle>{T.optOut}</AlertTitle>
              <AlertDescription>{T.optOutBody}</AlertDescription>
            </Alert>
          ) : null}
          <ClientCertificateFields form={form} stored={!!s?.clientCertificatePem} />
        </FormGroup>

        <FormGroup legend={T.sshTunnel} lead={T.sshLead}>
          <FormField
            control={form.control}
            name="sshEnabled"
            render={({ field }) => (
              <FormItem className="flex flex-row items-center gap-3">
                <FormControl>
                  <Switch checked={field.value} onCheckedChange={field.onChange} />
                </FormControl>
                <FormLabel className="font-normal">{T.sshSwitch}</FormLabel>
              </FormItem>
            )}
          />
          {editing && original.sshEnabled && !values.sshEnabled ? (
            <p className="text-sm text-status-partial-fg">{T.sshRemoved}</p>
          ) : null}
          {values.sshEnabled ? (
            <>
              <div className="grid gap-5 sm:grid-cols-[minmax(0,1fr)_7rem]">
                <FormField
                  control={form.control}
                  name="ssh.host"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{T.bastionHost}</FormLabel>
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
                      <FormLabel>{T.port}</FormLabel>
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
                    <FormLabel>{T.sshUser}</FormLabel>
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

        <FormGroup legend={T.runLimit} lead={T.runLimitLead}>
          <FormField
            control={form.control}
            name="maxConcurrentRuns"
            render={({ field }) => (
              <FormItem className="max-w-56">
                <FormLabel>{T.concurrentRuns}</FormLabel>
                <FormControl>
                  <Input inputMode="numeric" placeholder={T.globalDefault} {...field} />
                </FormControl>
                <FormDescription>
                  {T.runsHint(original?.effectiveMaxConcurrentRuns)}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </FormGroup>

        <div className="sticky bottom-0 flex justify-end gap-2 border-t bg-background/95 py-4 backdrop-blur-sm">
          <Button asChild variant="outline">
            <Link to={cancelTo}>{T.cancel}</Link>
          </Button>
          <Button type="submit" disabled={submitting}>
            {submitting ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
            {editing ? T.saveChanges : T.create}
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
            ? T.clientCertStored
            : action === "clear"
              ? T.clientCertRemoved
              : T.noClientCert}
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
            {T.undo}
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
            {action === "keep" ? T.replace : T.addClientCert}
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
            {T.remove}
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
            <FormLabel>{T.clientCertPem}</FormLabel>
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
            <FormLabel>{T.clientKeyPem}</FormLabel>
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
        {T.cancel}
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
    ...(stored && !reentry ? [{ value: "keep" as const, label: T.keepCredential }] : []),
    ...(!stored ? [{ value: "later" as const, label: T.generateLater }] : []),
    { value: "privateKey", label: T.pasteKey },
    { value: "password", label: T.aPassword },
  ];
  return (
    <div className="flex flex-col gap-4">
      {reentry ? (
        <Alert>
          <AlertDescription>{T.sshReentry}</AlertDescription>
        </Alert>
      ) : null}
      <fieldset className="flex flex-col gap-2">
        <legend className="mb-1 text-sm font-medium">{T.sshSignIn}</legend>
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
              <FormLabel>{T.sshPassword}</FormLabel>
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
                <FormLabel>{T.privateKey}</FormLabel>
                <FormControl>
                  <textarea
                    rows={5}
                    spellCheck={false}
                    autoComplete="off"
                    className={cn(textarea, "[-webkit-text-security:disc]")}
                    {...field}
                  />
                </FormControl>
                <FormDescription>{T.preferGenerate}</FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name="ssh.privateKeyPassphrase"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{T.passphrase}</FormLabel>
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
