import { useMutation } from "@apollo/client/react";
import { zodResolver } from "@hookform/resolvers/zod";
import { Cloud, FolderTree, Loader2, MapPinned, Server } from "lucide-react";
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
import { cn } from "@/lib/utils";

import {
  buildSchema,
  type DestinationFormValues,
  effectiveSftpAuth,
  emptyValues,
  FORM_FIELDS,
  type Original,
  SERVER_FIELDS,
  sftpNeedsPassword,
  toCreateInput,
  toUpdateInput,
  valuesFrom,
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

const textarea =
  "w-full rounded-md border border-input bg-surface px-3 py-2 font-mono text-xs shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 aria-invalid:border-destructive dark:bg-surface-sunken";

const TYPE_ICON = { S3: Cloud, SFTP: Server, DISK: FolderTree } as const;

type FormApi = ReturnType<typeof useForm<DestinationFormValues>>;

/**
 * D-209: every destination owns its location (no two destinations share
 * or nest one), and the server refuses a location change (path, bucket,
 * prefix, endpoint, region, host, port, user, base path, subdirectory)
 * while backups are stored there or a run is writing to it.
 */
const LOCATION_NOTE = {
  own: "Use a location of its own: never the same path, bucket prefix or directory as another destination, nor one inside another. To take over backups already stored somewhere, run bekup reindex --import.",
  fixed:
    "The location can only change while no backups are stored here and no run is writing here: bekup would lose track of the files already there.",
} as const;

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
    | "sftp.privateKeyPassphrase";
  label: string;
  description?: string;
  placeholder?: string;
  mono?: boolean;
  type?: "text" | "password";
  className?: string;
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
    <fieldset className="grid gap-3 sm:grid-cols-3">
      <legend className="sr-only">Destination type</legend>
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
      <FormGroup legend="Bucket" lead="Leave the endpoint empty for AWS S3.">
        <TextField
          form={form}
          name="s3.endpoint"
          label="Endpoint URL"
          placeholder="https://s3.example.com"
          mono
        />
        <div className="grid gap-5 sm:grid-cols-2">
          <TextField form={form} name="s3.region" label="Region" placeholder="eu-central-1" mono />
          <TextField form={form} name="s3.bucket" label="Bucket" mono />
        </div>
        <TextField
          form={form}
          name="s3.prefix"
          label="Key prefix"
          description="Optional; empty writes to the bucket root."
          mono
        />
        <LocationNote editing={Boolean(original)} />
        <FormField
          control={form.control}
          name="s3.pathStyle"
          render={({ field }) => (
            <FormItem className="flex flex-row items-center gap-3">
              <FormControl>
                <Switch checked={field.value} onCheckedChange={field.onChange} />
              </FormControl>
              <FormLabel className="font-normal">
                Path-style addressing (MinIO and other self-hosted stores)
              </FormLabel>
            </FormItem>
          )}
        />
      </FormGroup>

      <FormGroup
        legend="Access"
        lead="The secret access key is write-only: stored encrypted, never shown again."
      >
        <TextField form={form} name="s3.accessKeyId" label="Access key ID" mono />
        <WriteOnlyField
          label="Secret access key"
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
            label={storedSecret ? "New secret access key" : "Secret access key"}
            type="password"
          />
        </WriteOnlyField>
        <FormField
          control={form.control}
          name="s3.caBundlePem"
          render={({ field }) => (
            <FormItem>
              <FormLabel>Custom CA bundle (PEM, optional)</FormLabel>
              <FormControl>
                <textarea rows={4} spellCheck={false} className={textarea} {...field} />
              </FormControl>
              <FormDescription>
                Replaces the system roots for this endpoint (a self-hosted store with a private CA).
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
      </FormGroup>

      <FormGroup legend="Storage">
        <div className="grid gap-5 sm:grid-cols-2">
          <FormField
            control={form.control}
            name="s3.encryption"
            render={({ field }) => (
              <FormItem>
                <FormLabel>Server-side encryption</FormLabel>
                <FormControl>
                  <NativeSelect {...field}>
                    {(["SSE_S3", "SSE_KMS", "NONE"] as const).map((e) => (
                      <option key={e} value={e}>
                        {ENCRYPTION_LABEL[e]}
                      </option>
                    ))}
                  </NativeSelect>
                </FormControl>
                <FormDescription>Backups are already encrypted by bekup.</FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          {s3.encryption === "SSE_KMS" ? (
            <TextField
              form={form}
              name="s3.kmsKeyId"
              label="KMS key ID"
              description="Optional: the account's default key."
              mono
            />
          ) : null}
          <TextField
            form={form}
            name="s3.storageClass"
            label="Storage class"
            placeholder="STANDARD"
            mono
          />
          <FormField
            control={form.control}
            name="s3.checksumMode"
            render={({ field }) => (
              <FormItem>
                <FormLabel>Checksums</FormLabel>
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
                <FormLabel className="font-normal">Never overwrite (If-None-Match)</FormLabel>
                <FormDescription>
                  Turn off only for stores that reject conditional writes.
                </FormDescription>
              </div>
            </FormItem>
          )}
        />
      </FormGroup>

      <FormGroup
        legend="Object Lock"
        lead="Default retention on every upload. Needs a bucket created with Object Lock."
      >
        <FormField
          control={form.control}
          name="s3.objectLock"
          render={({ field }) => (
            <FormItem className="flex flex-row items-center gap-3">
              <FormControl>
                <Switch checked={field.value} onCheckedChange={field.onChange} />
              </FormControl>
              <FormLabel className="font-normal">Lock every upload</FormLabel>
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
                  <FormLabel>Mode</FormLabel>
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
            <TextField form={form} name="s3.objectLockRetentionDays" label="Retention (days)" />
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
    ...(storedAuth && !reentry
      ? [{ value: "keep" as const, label: "Keep the stored credential" }]
      : []),
    ...(!storedAuth
      ? [{ value: "later" as const, label: "Generate a keypair after saving (recommended)" }]
      : []),
    { value: "privateKey", label: "Paste a private key" },
    { value: "password", label: "A password" },
  ];
  return (
    <>
      <FormGroup
        legend="Server"
        lead="Changing the host or port removes the pinned host key; confirm it again afterwards."
      >
        <div className="grid gap-5 sm:grid-cols-[minmax(0,1fr)_7rem]">
          <TextField form={form} name="sftp.host" label="Host" mono />
          <TextField form={form} name="sftp.port" label="Port" placeholder="22" />
        </div>
        <TextField form={form} name="sftp.user" label="User" mono />
        <div className="grid gap-5 sm:grid-cols-2">
          <TextField
            form={form}
            name="sftp.basePath"
            label="Base path"
            placeholder="/srv/backups"
            mono
          />
          <TextField
            form={form}
            name="sftp.dir"
            label="Subdirectory"
            description="Optional, below the base path."
            mono
          />
        </div>
        <LocationNote editing={Boolean(original)} />
      </FormGroup>
      <FormGroup legend="Sign-in" lead="Credentials are write-only.">
        {reentry ? (
          <Alert>
            <AlertDescription>
              The server, user or base path changed. Enter the password again or switch to a key:
              bekup never sends a stored password to a new endpoint.
            </AlertDescription>
          </Alert>
        ) : null}
        <fieldset className="flex flex-col gap-2">
          <legend className="mb-1 text-sm font-medium">Authenticate with</legend>
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
          <TextField form={form} name="sftp.password" label="Password" type="password" />
        ) : null}
        {auth === "privateKey" ? (
          <>
            <FormField
              control={form.control}
              name="sftp.privateKey"
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
                  <FormMessage />
                </FormItem>
              )}
            />
            <TextField
              form={form}
              name="sftp.privateKeyPassphrase"
              label="Key passphrase (optional)"
              type="password"
            />
          </>
        ) : null}
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
        toast.success("Destination saved.");
        void navigate(`/destinations/${original.id}`);
      } else {
        const r = await withStepUp(() => create({ variables: { input: toCreateInput(v) } }));
        const id = r.data?.createDestination.id;
        toast.success(
          v.type === "SFTP"
            ? "Destination created. Next: confirm the server's host key."
            : "Destination created. Test it before the first backup.",
        );
        void navigate(id ? `/destinations/${id}` : "/destinations");
      }
    } catch (err) {
      if (!applyFieldErrors(err, form.setError, FORM_FIELDS, SERVER_FIELDS)) {
        reportActionError(err);
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
        aria-label={editing ? "Edit destination" : "New destination"}
      >
        <FormGroup legend="Type">
          {editing ? (
            <p className="text-sm">
              <span className="font-medium">{TYPE_LABEL[type]}.</span>{" "}
              <span className="text-muted-foreground">The type cannot change.</span>
            </p>
          ) : (
            <TypePicker form={form} />
          )}
        </FormGroup>
        <FormGroup legend="Identity">
          <TextField form={form} name="name" label="Name" />
          <FormField
            control={form.control}
            name="noDelete"
            render={({ field }) => (
              <FormItem className="flex flex-row items-start gap-3">
                <FormControl>
                  <Switch checked={field.value} onCheckedChange={field.onChange} />
                </FormControl>
                <div className="flex flex-col gap-1">
                  <FormLabel className="font-normal">No-delete mode</FormLabel>
                  <FormDescription>
                    bekup never deletes from this destination; retention is left to lifecycle rules
                    (pair it with write-only credentials).
                  </FormDescription>
                </div>
              </FormItem>
            )}
          />
        </FormGroup>
        {type === "DISK" ? (
          <FormGroup legend="Directory">
            <TextField
              form={form}
              name="disk.path"
              label="Directory below the disk root"
              placeholder="nightly"
              description="Relative to storage.disk_root on the server; no “..”."
              mono
            />
            <LocationNote editing={editing} />
          </FormGroup>
        ) : null}
        {type === "S3" ? <S3Fields form={form} original={original} /> : null}
        {type === "SFTP" ? <SftpFields form={form} original={original} /> : null}

        <div className="sticky bottom-0 flex justify-end gap-2 border-t bg-background/95 py-4 backdrop-blur-sm">
          <Button asChild variant="outline">
            <Link to={original ? `/destinations/${original.id}` : "/destinations"}>Cancel</Link>
          </Button>
          <Button type="submit" disabled={submitting}>
            {submitting ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
            {editing ? "Save changes" : "Create destination"}
          </Button>
        </div>
      </form>
    </Form>
  );
}
