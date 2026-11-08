import { useMutation } from "@apollo/client/react";
import { zodResolver } from "@hookform/resolvers/zod";
import { Cloud, FolderTree, Globe, Loader2, MapPinned, Server, ShieldAlert } from "lucide-react";
import { useId, useMemo } from "react";
import { useForm, useWatch, type Resolver } from "react-hook-form";
import { Link, useNavigate } from "react-router";
import { toast } from "sonner";

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
import { NativeSelect } from "@/components/ui/native-select";
import { Switch } from "@/components/ui/switch";
import { reportActionError, useStepUp } from "@/features/auth/use-step-up";
import { FormGroup } from "@/features/connections/ui";
import { WriteOnlyField } from "@/features/connections/write-only-field";
import { applyFieldErrors } from "@/lib/api-error";
import { defineMessages } from "@/lib/i18n";
import { cn } from "@/lib/utils";

import {
  buildSchema,
  type DestinationFormValues,
  effectiveSftpAuth,
  effectiveWebdavPasswordAction,
  emptyValues,
  FORM_FIELDS,
  type Original,
  SERVER_FIELDS,
  sftpNeedsPassword,
  toCreateInput,
  toUpdateInput,
  valuesFrom,
  webdavNeedsPassword,
} from "./form-model";
import {
  CHECKSUM_LABEL,
  ENCRYPTION_LABEL,
  LOCK_DESCRIPTION,
  LOCK_LABEL,
  TYPE_DESCRIPTION,
  TYPE_LABEL,
  TYPES,
} from "./labels";
import {
  CreateDestinationMutation,
  DestinationDetailQuery,
  DestinationsListQuery,
  UpdateDestinationMutation,
} from "./operations";
import { FLAVOR_DESCRIPTION, FLAVOR_LABEL, PATH_HELP, URL_HELP, WT } from "./webdav-text";

const textarea =
  "w-full rounded-md border border-input bg-surface px-3 py-2 font-mono text-xs shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 aria-invalid:border-destructive dark:bg-surface-sunken";

const TYPE_ICON = { S3: Cloud, SFTP: Server, WEBDAV: Globe, DISK: FolderTree } as const;

type FormApi = ReturnType<typeof useForm<DestinationFormValues>>;

const F = defineMessages({
  en: {
    locationOwn:
      "Use a location of its own: never the same path, bucket prefix or directory as another destination, nor one inside another. To take over backups already stored somewhere, run bekup reindex --import.",
    locationFixed:
      "The location can only change while no backups are stored here and no run is writing here: bekup would lose track of the files already there.",
    typeLegend: "Destination type",
    bucketLegend: "Bucket",
    bucketLead: "Leave the endpoint empty for AWS S3.",
    endpoint: "Endpoint URL",
    region: "Region",
    bucket: "Bucket",
    prefix: "Key prefix",
    prefixHint: "Optional; empty writes to the bucket root.",
    pathStyle: "Path-style addressing (MinIO and other self-hosted stores)",
    accessLegend: "Access",
    accessLead: "The secret access key is write-only: stored encrypted, never shown again.",
    accessKeyId: "Access key ID",
    secretAccessKey: "Secret access key",
    newSecretAccessKey: "New secret access key",
    caBundle: "Custom CA bundle (PEM, optional)",
    caBundleHint:
      "Replaces the system roots for this endpoint (a self-hosted store with a private CA).",
    storageLegend: "Storage",
    sse: "Server-side encryption",
    sseHint: "Backups are already encrypted by bekup.",
    kmsKeyId: "KMS key ID",
    kmsKeyIdHint: "Optional: the account's default key.",
    storageClass: "Storage class",
    checksums: "Checksums",
    conditionalWrites: "Never overwrite (If-None-Match)",
    conditionalWritesHint: "Turn off only for stores that reject conditional writes.",
    objectLockLegend: "Object Lock",
    objectLockLead: "Default retention on every upload. Needs a bucket created with Object Lock.",
    objectLock: "Lock every upload",
    lockMode: "Mode",
    lockDays: "Retention (days)",
    authKeep: "Keep the stored credential",
    authLater: "Generate a keypair after saving (recommended)",
    authKey: "Paste a private key",
    authPassword: "A password",
    sftpServerLegend: "Server",
    sftpServerLead:
      "Changing the host or port removes the pinned host key; confirm it again afterwards.",
    host: "Host",
    port: "Port",
    user: "User",
    basePath: "Base path",
    dir: "Subdirectory",
    dirHint: "Optional, below the base path.",
    signInLegend: "Sign-in",
    signInLead: "Credentials are write-only.",
    sftpReentry:
      "The server, user or base path changed. Enter the password again or switch to a key: bekup never sends a stored password to a new endpoint.",
    authWith: "Authenticate with",
    password: "Password",
    privateKey: "Private key (write-only)",
    keyPassphrase: "Key passphrase (optional)",
    saved: "Destination saved.",
    createdSftp: "Destination created. Next: confirm the server's host key.",
    createdWebdav:
      "Destination created. Test it: a self-signed certificate can be pinned from the result.",
    created: "Destination created. Test it before the first backup.",
    editLabel: "Edit destination",
    newLabel: "New destination",
    typeGroup: "Type",
    typeFixed: "The type cannot change.",
    identityLegend: "Identity",
    name: "Name",
    noDelete: "No-delete mode",
    noDeleteHint:
      "bekup never deletes from this destination; retention is left to lifecycle rules (pair it with write-only credentials).",
    directoryLegend: "Directory",
    diskPath: "Directory below the disk root",
    diskPathHint: "Relative to storage.disk_root on the server; no “..”.",
    diskPathExample: "nightly",
    cancel: "Cancel",
    saveChanges: "Save changes",
    create: "Create destination",
  },
  nl: {
    locationOwn:
      "Gebruik een eigen locatie: nooit hetzelfde pad, dezelfde bucketprefix of map als een andere bestemming, en ook niet een locatie daarbinnen. Wil je back-ups overnemen die al ergens staan, voer dan bekup reindex --import uit.",
    locationFixed:
      "De locatie kan alleen wijzigen zolang hier geen back-ups staan en geen run hier schrijft: bekup zou de bestanden die er al staan uit het oog verliezen.",
    typeLegend: "Type bestemming",
    bucketLegend: "Bucket",
    bucketLead: "Laat het eindpunt leeg voor AWS S3.",
    endpoint: "Eindpunt-URL",
    region: "Regio",
    bucket: "Bucket",
    prefix: "Sleutelprefix",
    prefixHint: "Optioneel; leeg schrijft naar de root van de bucket.",
    pathStyle: "Path-style adressering (MinIO en andere zelf gehoste opslag)",
    accessLegend: "Toegang",
    accessLead:
      "De secret access key is alleen-schrijven: versleuteld opgeslagen en nooit meer getoond.",
    accessKeyId: "Access key ID",
    secretAccessKey: "Secret access key",
    newSecretAccessKey: "Nieuwe secret access key",
    caBundle: "Eigen CA-bundel (PEM, optioneel)",
    caBundleHint:
      "Vervangt de systeemroots voor dit eindpunt (zelf gehoste opslag met een privé-CA).",
    storageLegend: "Opslag",
    sse: "Versleuteling aan de serverkant",
    sseHint: "Back-ups zijn al versleuteld door bekup.",
    kmsKeyId: "KMS-sleutel-ID",
    kmsKeyIdHint: "Optioneel: de standaardsleutel van het account.",
    storageClass: "Opslagklasse",
    checksums: "Checksums",
    conditionalWrites: "Nooit overschrijven (If-None-Match)",
    conditionalWritesHint:
      "Zet dit alleen uit voor opslag die voorwaardelijke schrijfacties weigert.",
    objectLockLegend: "Object Lock",
    objectLockLead:
      "Standaardbewaartermijn bij elke upload. Vereist een bucket die met Object Lock is aangemaakt.",
    objectLock: "Elke upload vergrendelen",
    lockMode: "Modus",
    lockDays: "Bewaartermijn (dagen)",
    authKeep: "Opgeslagen inloggegevens behouden",
    authLater: "Na het opslaan een sleutelpaar genereren (aanbevolen)",
    authKey: "Een privésleutel plakken",
    authPassword: "Een wachtwoord",
    sftpServerLegend: "Server",
    sftpServerLead:
      "Als je de host of poort wijzigt, verdwijnt de vastgepinde hostsleutel; bevestig hem daarna opnieuw.",
    host: "Host",
    port: "Poort",
    user: "Gebruiker",
    basePath: "Basispad",
    dir: "Submap",
    dirHint: "Optioneel, onder het basispad.",
    signInLegend: "Inloggen",
    signInLead: "Inloggegevens zijn alleen-schrijven.",
    sftpReentry:
      "De server, de gebruiker of het basispad is gewijzigd. Vul het wachtwoord opnieuw in of stap over op een sleutel: bekup stuurt een opgeslagen wachtwoord nooit naar een nieuw eindpunt.",
    authWith: "Authenticeren met",
    password: "Wachtwoord",
    privateKey: "Privésleutel (alleen-schrijven)",
    keyPassphrase: "Wachtwoordzin van de sleutel (optioneel)",
    saved: "Bestemming opgeslagen.",
    createdSftp: "Bestemming aangemaakt. Volgende stap: bevestig de hostsleutel van de server.",
    createdWebdav:
      "Bestemming aangemaakt. Test haar: een zelfondertekend certificaat kun je vanuit het resultaat vastpinnen.",
    created: "Bestemming aangemaakt. Test haar vóór de eerste back-up.",
    editLabel: "Bestemming bewerken",
    newLabel: "Nieuwe bestemming",
    typeGroup: "Type",
    typeFixed: "Het type kan niet wijzigen.",
    identityLegend: "Identiteit",
    name: "Naam",
    noDelete: "Niet-verwijderen-modus",
    noDeleteHint:
      "bekup verwijdert nooit iets van deze bestemming; het bewaarbeleid wordt overgelaten aan lifecycle-regels (combineer dit met inloggegevens die alleen mogen schrijven).",
    directoryLegend: "Map",
    diskPath: "Map onder de schijfroot",
    diskPathHint: "Relatief ten opzichte van storage.disk_root op de server; geen „..”.",
    diskPathExample: "nachtelijk",
    cancel: "Annuleren",
    saveChanges: "Wijzigingen opslaan",
    create: "Bestemming maken",
  },
});

/**
 * D-209: every destination owns its location (no two destinations share
 * or nest one), and the server refuses a location change (path, bucket,
 * prefix, endpoint, region, host, port, user, base path, subdirectory)
 * while backups are stored there or a run is writing to it.
 */
/** Technical example values (the same in every language). */
const EXAMPLE = {
  endpoint: "https://s3.example.com",
  region: "eu-central-1",
  storageClass: "STANDARD",
  basePath: "/srv/backups",
};

const LOCATION_NOTE = { own: F.locationOwn, fixed: F.locationFixed };

function LocationNote({ editing = true }: { editing?: boolean }) {
  return (
    <p data-testid="location-note" className="flex items-start gap-2 text-xs text-muted-foreground">
      <MapPinned aria-hidden="true" className="mt-px size-3.5 shrink-0" />
      <span>
        {editing ? `${LOCATION_NOTE.fixed} ` : null}
        {LOCATION_NOTE.own}
      </span>
    </p>
  );
}

function TextField({
  form,
  name,
  label,
  description,
  placeholder,
  mono = false,
  type = "text",
  className,
  inputMode,
}: {
  form: FormApi;
  name:
    | "name"
    | "disk.path"
    | "s3.endpoint"
    | "s3.region"
    | "s3.bucket"
    | "s3.prefix"
    | "s3.kmsKeyId"
    | "s3.storageClass"
    | "s3.objectLockRetentionDays"
    | "s3.accessKeyId"
    | "s3.secretAccessKey"
    | "sftp.host"
    | "sftp.port"
    | "sftp.user"
    | "sftp.basePath"
    | "sftp.dir"
    | "sftp.password"
    | "sftp.privateKeyPassphrase"
    | "webdav.url"
    | "webdav.username"
    | "webdav.password"
    | "webdav.path"
    | "webdav.timeoutSeconds";
  label: string;
  description?: string;
  placeholder?: string;
  mono?: boolean;
  type?: "text" | "password";
  className?: string;
  inputMode?: "numeric";
}) {
  return (
    <FormField
      control={form.control}
      name={name}
      render={({ field }) => (
        <FormItem {...(className ? { className } : {})}>
          <FormLabel>{label}</FormLabel>
          <FormControl>
            <Input
              type={type}
              autoComplete={type === "password" ? "new-password" : "off"}
              spellCheck={false}
              {...(placeholder ? { placeholder } : {})}
              {...(inputMode ? { inputMode } : {})}
              className={cn(mono && "font-mono text-xs")}
              {...field}
            />
          </FormControl>
          {description ? <FormDescription>{description}</FormDescription> : null}
          <FormMessage />
        </FormItem>
      )}
    />
  );
}

function TypePicker({ form }: { form: FormApi }) {
  const group = useId();
  const type = useWatch({ control: form.control, name: "type" });
  return (
    <fieldset className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
      <legend className="sr-only">{F.typeLegend}</legend>
      {TYPES.map((t) => {
        const Icon = TYPE_ICON[t];
        const id = `${group}-${t}`;
        return (
          <label
            key={t}
            htmlFor={id}
            className={cn(
              "flex cursor-pointer flex-col gap-2 rounded-md border bg-card p-4 transition-colors hover:border-border-strong",
              type === t && "border-primary ring-2 ring-primary/20",
            )}
          >
            <span className="flex items-center gap-2 font-medium">
              <input
                id={id}
                type="radio"
                name={group}
                value={t}
                checked={type === t}
                onChange={() => {
                  form.setValue("type", t);
                  form.clearErrors();
                }}
                className="size-4 accent-primary"
              />
              <Icon aria-hidden="true" className="size-4 text-muted-foreground" />
              {TYPE_LABEL[t]}
            </span>
            <span className="text-xs text-muted-foreground">{TYPE_DESCRIPTION[t]}</span>
          </label>
        );
      })}
    </fieldset>
  );
}

function S3Fields({ form, original }: { form: FormApi; original: Original | undefined }) {
  const s3 = useWatch({ control: form.control, name: "s3" });
  const storedSecret = original?.settings?.s3?.hasSecretAccessKey === true;
  return (
    <>
      <FormGroup legend={F.bucketLegend} lead={F.bucketLead}>
        <TextField
          form={form}
          name="s3.endpoint"
          label={F.endpoint}
          placeholder={EXAMPLE.endpoint}
          mono
        />
        <div className="grid gap-5 sm:grid-cols-2">
          <TextField
            form={form}
            name="s3.region"
            label={F.region}
            placeholder={EXAMPLE.region}
            mono
          />
          <TextField form={form} name="s3.bucket" label={F.bucket} mono />
        </div>
        <TextField form={form} name="s3.prefix" label={F.prefix} description={F.prefixHint} mono />
        <LocationNote editing={Boolean(original)} />
        <FormField
          control={form.control}
          name="s3.pathStyle"
          render={({ field }) => (
            <FormItem className="flex flex-row items-center gap-3">
              <FormControl>
                <Switch checked={field.value} onCheckedChange={field.onChange} />
              </FormControl>
              <FormLabel className="font-normal">{F.pathStyle}</FormLabel>
            </FormItem>
          )}
        />
      </FormGroup>

      <FormGroup legend={F.accessLegend} lead={F.accessLead}>
        <TextField form={form} name="s3.accessKeyId" label={F.accessKeyId} mono />
        <WriteOnlyField
          label={F.secretAccessKey}
          stored={storedSecret}
          action={s3.secretAction}
          onAction={(a) => {
            form.setValue("s3.secretAction", a === "clear" ? "keep" : a);
            form.setValue("s3.secretAccessKey", "");
          }}
        >
          <TextField
            form={form}
            name="s3.secretAccessKey"
            label={storedSecret ? F.newSecretAccessKey : F.secretAccessKey}
            type="password"
          />
        </WriteOnlyField>
        <FormField
          control={form.control}
          name="s3.caBundlePem"
          render={({ field }) => (
            <FormItem>
              <FormLabel>{F.caBundle}</FormLabel>
              <FormControl>
                <textarea rows={4} spellCheck={false} className={textarea} {...field} />
              </FormControl>
              <FormDescription>{F.caBundleHint}</FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
      </FormGroup>

      <FormGroup legend={F.storageLegend}>
        <div className="grid gap-5 sm:grid-cols-2">
          <FormField
            control={form.control}
            name="s3.encryption"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{F.sse}</FormLabel>
                <FormControl>
                  <NativeSelect {...field}>
                    {(["SSE_S3", "SSE_KMS", "NONE"] as const).map((e) => (
                      <option key={e} value={e}>
                        {ENCRYPTION_LABEL[e]}
                      </option>
                    ))}
                  </NativeSelect>
                </FormControl>
                <FormDescription>{F.sseHint}</FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          {s3.encryption === "SSE_KMS" ? (
            <TextField
              form={form}
              name="s3.kmsKeyId"
              label={F.kmsKeyId}
              description={F.kmsKeyIdHint}
              mono
            />
          ) : null}
          <TextField
            form={form}
            name="s3.storageClass"
            label={F.storageClass}
            placeholder={EXAMPLE.storageClass}
            mono
          />
          <FormField
            control={form.control}
            name="s3.checksumMode"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{F.checksums}</FormLabel>
                <FormControl>
                  <NativeSelect {...field}>
                    {(["WHEN_SUPPORTED", "WHEN_REQUIRED"] as const).map((c) => (
                      <option key={c} value={c}>
                        {CHECKSUM_LABEL[c]}
                      </option>
                    ))}
                  </NativeSelect>
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
        </div>
        <FormField
          control={form.control}
          name="s3.conditionalWrites"
          render={({ field }) => (
            <FormItem className="flex flex-row items-start gap-3">
              <FormControl>
                <Switch checked={field.value} onCheckedChange={field.onChange} />
              </FormControl>
              <div className="flex flex-col gap-1">
                <FormLabel className="font-normal">{F.conditionalWrites}</FormLabel>
                <FormDescription>{F.conditionalWritesHint}</FormDescription>
              </div>
            </FormItem>
          )}
        />
      </FormGroup>

      <FormGroup legend={F.objectLockLegend} lead={F.objectLockLead}>
        <FormField
          control={form.control}
          name="s3.objectLock"
          render={({ field }) => (
            <FormItem className="flex flex-row items-center gap-3">
              <FormControl>
                <Switch checked={field.value} onCheckedChange={field.onChange} />
              </FormControl>
              <FormLabel className="font-normal">{F.objectLock}</FormLabel>
            </FormItem>
          )}
        />
        {s3.objectLock ? (
          <div className="grid gap-5 sm:grid-cols-2">
            <FormField
              control={form.control}
              name="s3.objectLockMode"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{F.lockMode}</FormLabel>
                  <FormControl>
                    <NativeSelect {...field}>
                      {(["GOVERNANCE", "COMPLIANCE"] as const).map((m) => (
                        <option key={m} value={m}>
                          {LOCK_LABEL[m]}
                        </option>
                      ))}
                    </NativeSelect>
                  </FormControl>
                  <FormDescription>{LOCK_DESCRIPTION[s3.objectLockMode]}</FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <TextField form={form} name="s3.objectLockRetentionDays" label={F.lockDays} />
          </div>
        ) : null}
      </FormGroup>
    </>
  );
}

function SftpFields({ form, original }: { form: FormApi; original: Original | undefined }) {
  const values = useWatch({ control: form.control }) as DestinationFormValues;
  const auth = effectiveSftpAuth(values, original);
  const reentry = sftpNeedsPassword(values, original);
  const storedAuth = (original?.settings?.sftp?.authMethod ?? "NONE") !== "NONE";
  const group = useId();
  const choices: { value: DestinationFormValues["sftp"]["auth"]; label: string }[] = [
    ...(storedAuth && !reentry ? [{ value: "keep" as const, label: F.authKeep }] : []),
    ...(!storedAuth ? [{ value: "later" as const, label: F.authLater }] : []),
    { value: "privateKey", label: F.authKey },
    { value: "password", label: F.authPassword },
  ];
  return (
    <>
      <FormGroup legend={F.sftpServerLegend} lead={F.sftpServerLead}>
        <div className="grid gap-5 sm:grid-cols-[minmax(0,1fr)_7rem]">
          <TextField form={form} name="sftp.host" label={F.host} mono />
          <TextField form={form} name="sftp.port" label={F.port} placeholder="22" />
        </div>
        <TextField form={form} name="sftp.user" label={F.user} mono />
        <div className="grid gap-5 sm:grid-cols-2">
          <TextField
            form={form}
            name="sftp.basePath"
            label={F.basePath}
            placeholder={EXAMPLE.basePath}
            mono
          />
          <TextField form={form} name="sftp.dir" label={F.dir} description={F.dirHint} mono />
        </div>
        <LocationNote editing={Boolean(original)} />
      </FormGroup>
      <FormGroup legend={F.signInLegend} lead={F.signInLead}>
        {reentry ? (
          <Alert>
            <AlertDescription>{F.sftpReentry}</AlertDescription>
          </Alert>
        ) : null}
        <fieldset className="flex flex-col gap-2">
          <legend className="mb-1 text-sm font-medium">{F.authWith}</legend>
          {choices.map((c) => (
            <label key={c.value} className="flex items-center gap-2 text-sm">
              <input
                type="radio"
                name={group}
                value={c.value}
                checked={auth === c.value}
                onChange={() => {
                  form.setValue("sftp.auth", c.value);
                  form.setValue("sftp.password", "");
                  form.setValue("sftp.privateKey", "");
                  form.setValue("sftp.privateKeyPassphrase", "");
                }}
                className="size-4 accent-primary"
              />
              {c.label}
            </label>
          ))}
        </fieldset>
        {auth === "password" ? (
          <TextField form={form} name="sftp.password" label={F.password} type="password" />
        ) : null}
        {auth === "privateKey" ? (
          <>
            <FormField
              control={form.control}
              name="sftp.privateKey"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{F.privateKey}</FormLabel>
                  <FormControl>
                    <textarea
                      rows={5}
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
            <TextField
              form={form}
              name="sftp.privateKeyPassphrase"
              label={F.keyPassphrase}
              type="password"
            />
          </>
        ) : null}
      </FormGroup>
    </>
  );
}

function FlavorPicker({ form }: { form: FormApi }) {
  const group = useId();
  const flavor = useWatch({ control: form.control, name: "webdav.flavor" });
  const error = form.formState.errors.webdav?.flavor?.message;
  return (
    <fieldset
      className="flex flex-col gap-2"
      aria-describedby={error ? `${group}-error` : undefined}
    >
      <legend className="mb-1 text-sm font-medium">{WT.form.flavor}</legend>
      <div className="grid gap-3 sm:grid-cols-2">
        {(["GENERIC", "NEXTCLOUD"] as const).map((f) => {
          const id = `${group}-${f}`;
          return (
            <label
              key={f}
              htmlFor={id}
              className={cn(
                "flex cursor-pointer flex-col gap-1.5 rounded-md border bg-card p-3.5 transition-colors hover:border-border-strong",
                flavor === f && "border-primary ring-2 ring-primary/20",
              )}
            >
              <span className="flex items-center gap-2 text-sm font-medium">
                <input
                  id={id}
                  type="radio"
                  name={group}
                  value={f}
                  checked={flavor === f}
                  onChange={() => {
                    form.setValue("webdav.flavor", f);
                    form.clearErrors("webdav.flavor");
                  }}
                  className="size-4 accent-primary"
                />
                {FLAVOR_LABEL[f]}
              </span>
              <span className="text-xs text-muted-foreground">{FLAVOR_DESCRIPTION[f]}</span>
            </label>
          );
        })}
      </div>
      {error ? (
        <p id={`${group}-error`} className="text-sm text-destructive">
          {error}
        </p>
      ) : null}
    </fieldset>
  );
}

function WebdavFields({ form, original }: { form: FormApi; original: Original | undefined }) {
  const values = useWatch({ control: form.control }) as DestinationFormValues;
  const flavor = values.webdav.flavor;
  const stored = original?.settings?.webdav;
  const reentry = webdavNeedsPassword(values, original);
  const action = effectiveWebdavPasswordAction(values, original);
  return (
    <>
      <FormGroup legend={WT.form.serverLegend} lead={WT.form.serverLead}>
        <FlavorPicker form={form} />
        <TextField
          form={form}
          name="webdav.url"
          label={WT.form.url}
          placeholder={URL_HELP[flavor].placeholder}
          description={URL_HELP[flavor].description}
          mono
        />
        <TextField
          form={form}
          name="webdav.path"
          label={WT.form.path}
          description={PATH_HELP[flavor]}
          mono
        />
        {stored?.trust === "PINNED" ? (
          <p className="flex items-start gap-2 text-xs text-muted-foreground">
            <ShieldAlert aria-hidden="true" className="mt-px size-3.5 shrink-0" />
            <span>{WT.form.pinNote}</span>
          </p>
        ) : null}
        <LocationNote editing={Boolean(original)} />
      </FormGroup>
      <FormGroup legend={WT.form.signInLegend} lead={WT.form.signInLead}>
        <TextField form={form} name="webdav.username" label={WT.form.username} mono />
        <WriteOnlyField
          label={WT.form.password}
          stored={stored?.hasPassword === true}
          action={action}
          onAction={(a) => {
            form.setValue("webdav.passwordAction", a === "clear" ? "keep" : a);
            form.setValue("webdav.password", "");
            form.clearErrors("webdav.password");
          }}
          reentry={reentry ? WT.form.reentry : undefined}
        >
          <TextField
            form={form}
            name="webdav.password"
            label={stored?.hasPassword ? WT.form.newPassword : WT.form.password}
            description={WT.form.passwordHint}
            type="password"
          />
        </WriteOnlyField>
      </FormGroup>
      <FormGroup legend={WT.form.tlsLegend} lead={WT.form.tlsLead}>
        <FormField
          control={form.control}
          name="webdav.caBundlePem"
          render={({ field }) => (
            <FormItem>
              <FormLabel>{WT.form.caBundle}</FormLabel>
              <FormControl>
                <textarea rows={4} spellCheck={false} className={textarea} {...field} />
              </FormControl>
              <FormDescription>{WT.form.caBundleHint}</FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
        <TextField
          form={form}
          name="webdav.timeoutSeconds"
          label={WT.form.timeout}
          description={WT.form.timeoutHint}
          placeholder="60"
          inputMode="numeric"
          className="sm:max-w-xs"
        />
      </FormGroup>
    </>
  );
}

/** Create or edit a destination (admins; step-up). */
export function DestinationForm({ original }: { original?: Original }) {
  const editing = original !== undefined;
  const navigate = useNavigate();
  const { withStepUp } = useStepUp();
  const schema = useMemo(() => buildSchema(original), [original]);
  const form = useForm<DestinationFormValues>({
    resolver: zodResolver(schema) as Resolver<DestinationFormValues>,
    defaultValues: original ? valuesFrom(original) : emptyValues(),
  });
  const type = useWatch({ control: form.control, name: "type" });
  const [create] = useMutation(CreateDestinationMutation, {
    refetchQueries: [DestinationsListQuery],
  });
  const [update] = useMutation(UpdateDestinationMutation, {
    refetchQueries: [DestinationsListQuery, DestinationDetailQuery],
    awaitRefetchQueries: true,
  });

  const onSubmit = async (v: DestinationFormValues) => {
    try {
      if (original) {
        await withStepUp(() =>
          update({ variables: { id: original.id, input: toUpdateInput(v, original) } }),
        );
        toast.success(F.saved);
        void navigate(`/destinations/${original.id}`);
      } else {
        const r = await withStepUp(() => create({ variables: { input: toCreateInput(v) } }));
        const id = r.data?.createDestination.id;
        toast.success(
          v.type === "SFTP" ? F.createdSftp : v.type === "WEBDAV" ? F.createdWebdav : F.created,
        );
        void navigate(id ? `/destinations/${id}` : "/destinations");
      }
    } catch (err) {
      if (!applyFieldErrors(err, form.setError, FORM_FIELDS, SERVER_FIELDS)) {
        reportActionError(err);
      } else if (form.getFieldState("webdav.password").error) {
        // Open the write-only input so the server's reason is visible.
        form.setValue("webdav.passwordAction", "set");
      }
    }
  };

  const submitting = form.formState.isSubmitting;

  return (
    <Form {...form}>
      <form
        onSubmit={(e) => void form.handleSubmit(onSubmit)(e)}
        noValidate
        className="flex flex-col"
        aria-label={editing ? F.editLabel : F.newLabel}
      >
        <FormGroup legend={F.typeGroup}>
          {editing ? (
            <p className="text-sm">
              <span className="font-medium">{TYPE_LABEL[type]}.</span>{" "}
              <span className="text-muted-foreground">{F.typeFixed}</span>
            </p>
          ) : (
            <TypePicker form={form} />
          )}
        </FormGroup>
        <FormGroup legend={F.identityLegend}>
          <TextField form={form} name="name" label={F.name} />
          <FormField
            control={form.control}
            name="noDelete"
            render={({ field }) => (
              <FormItem className="flex flex-row items-start gap-3">
                <FormControl>
                  <Switch checked={field.value} onCheckedChange={field.onChange} />
                </FormControl>
                <div className="flex flex-col gap-1">
                  <FormLabel className="font-normal">{F.noDelete}</FormLabel>
                  <FormDescription>{F.noDeleteHint}</FormDescription>
                </div>
              </FormItem>
            )}
          />
        </FormGroup>
        {type === "DISK" ? (
          <FormGroup legend={F.directoryLegend}>
            <TextField
              form={form}
              name="disk.path"
              label={F.diskPath}
              placeholder={F.diskPathExample}
              description={F.diskPathHint}
              mono
            />
            <LocationNote editing={editing} />
          </FormGroup>
        ) : null}
        {type === "S3" ? <S3Fields form={form} original={original} /> : null}
        {type === "SFTP" ? <SftpFields form={form} original={original} /> : null}
        {type === "WEBDAV" ? <WebdavFields form={form} original={original} /> : null}

        <div className="sticky bottom-0 flex justify-end gap-2 border-t bg-background/95 py-4 backdrop-blur-sm">
          <Button asChild variant="outline">
            <Link to={original ? `/destinations/${original.id}` : "/destinations"}>{F.cancel}</Link>
          </Button>
          <Button type="submit" disabled={submitting}>
            {submitting ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
            {editing ? F.saveChanges : F.create}
          </Button>
        </div>
      </form>
    </Form>
  );
}
