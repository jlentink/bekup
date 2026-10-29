import { useMutation, useQuery } from "@apollo/client/react";
import { zodResolver } from "@hookform/resolvers/zod";
import {
  ArrowLeft,
  BellRing,
  FlaskConical,
  KeyRound,
  Loader2,
  ShieldCheck,
  Users,
} from "lucide-react";
import { useCallback, useState, type ReactNode } from "react";
import { useForm, useWatch } from "react-hook-form";
import { Link, useNavigate, useParams } from "react-router";
import { toast } from "sonner";
import { z } from "zod";

import { PageHeader } from "@/components/page-header";
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
import { Skeleton } from "@/components/ui/skeleton";
import { reportActionError, useStepUp } from "@/features/auth/use-step-up";
import type {
  CreateJobInput,
  JobDetailQuery as JobDetailResult,
  JobEncryptionInput,
  UpdateJobInput,
} from "@/graphql/gen/graphql";
import { apiErrors, applyFieldErrors, userMessage } from "@/lib/api-error";
import { cn } from "@/lib/utils";

import { EncryptionPanel, MIN_JOB_PASSPHRASE } from "./encryption-panel";
import { CheckboxField, NativeSelect } from "./fields";
import { OneTimeKeyDialog } from "./one-time-key";
import { identityKey, passphraseKey, type OneTimeKey } from "./one-time-key-model";
import {
  CreateJobMutation,
  JobDetailQuery,
  JobEditorOptionsQuery,
  JobsListQuery,
  UpdateJobMutation,
} from "./operations";
import { JobNotificationsPanel } from "./notifications-panel";
import { OwnersPanel } from "./owners-panel";
import { TestRestoreSettings } from "./test-restore-settings";
import { DatabasePicker, DestinationPicker } from "./pickers";
import { useRoleFlags } from "./roles";
import { defaultDraft, draftFromSchedule, draftToInput, type ScheduleDraft } from "./schedule";
import { ScheduleBuilder } from "./schedule-builder";

type Job = NonNullable<JobDetailResult["job"]>;

const AGE_RECIPIENT_RE = /^age1[02-9ac-hj-np-z]{58}$/;
const SLUG_RE = /^[a-z0-9]+(-[a-z0-9]+)*$/;

const T = {
  newEyebrow: "New job",
  newTitle: "Create a backup job",
  editEyebrow: "Edit job",
  back: "Back to jobs",
  backJob: "Back to the job",
  sections: {
    basics: "Job",
    source: "Source",
    schedule: "Schedule",
    retention: "Retention and limits",
    destinations: "Destinations",
    encryption: "Encryption",
    owners: "Backup owners",
    testRestore: "Test restores",
    notifications: "Notifications",
  },
  leads: {
    basics:
      "A name for people; the slug names the job's directory on every destination and never changes.",
    source:
      "The connection and the databases to dump. Temporary restore connections are not offered.",
    schedule: "When bekup runs the job on its own. You can always start it by hand.",
    retention: "How many backups each destination keeps. At least one limit is required.",
    destinations: "Uploads fan out in this order. 1 to 16 destinations.",
    encryption: "Backups are always encrypted with age unless an administrator turns it off.",
    owners: "Owners with role User can view and run this job and download its backups.",
    testRestore:
      "Prove the backups restore: a regular restore into a scratch database, compared with the source statistics.",
    notifications:
      "Shared channels that receive this job's events, and a shortcut to your own per-job preferences.",
  },
  name: "Name",
  slug: "Slug",
  slugHint: "Optional; derived from the name. Lower-case letters, digits and inner hyphens.",
  slugFixed: "Fixed when the job was created.",
  description: "Description",
  connection: "Connection",
  chooseConnection: "Choose a connection",
  dbMode: "Databases",
  dbModeAll: "Exclude",
  dbModeSelected: "Select only",
  includeGlobals: "Include cluster globals",
  includeGlobalsHint: "Roles and tablespaces (pg_dumpall --globals-only). PostgreSQL only.",
  keepLast: "Keep the newest",
  keepLastHint: "backups",
  keepDays: "Keep backups younger than",
  keepDaysHint: "days",
  retentionRequired: "Set at least one limit.",
  positive: "Enter a whole number of 1 or more.",
  timeout: "Run timeout (minutes)",
  timeoutHint: "Empty: the server default (workers.backup_timeout).",
  verification: "Completeness evidence",
  verificationStandard: "Standard: object counts and row estimates",
  verificationDeep: "Deep: exact per-table row counts",
  verificationHint:
    "Deep only adds exactness for PostgreSQL: MySQL and MariaDB always record estimates (D-153). Deep costs a count(*) per table at dump time.",
  enabled: "Enable the schedule right away",
  keyChoice: "Key",
  keyGeneratePassphrase: "Generate a passphrase (recommended)",
  keyGenerateKeypair: "Generate an age keypair (private key shown once)",
  keyPassphrase: "Use my passphrase",
  keyRecipient: "Encrypt to my age recipient",
  keyAdminNote:
    "Choosing your own key or a keypair is an admin action and needs a fresh identity check.",
  keyDefaultNote:
    "bekup generates a strong passphrase and shows it to you once. Administrators can change keys later.",
  passphrase: "Passphrase",
  passphraseTooShort: `At least ${String(MIN_JOB_PASSPHRASE)} characters.`,
  recipient: "Recipient public key",
  recipientInvalid: "Enter an age X25519 public key (age1…).",
  keyLabel: "Label (optional)",
  encryptionAfter: "Key changes, the opt-out and the passphrase export are on the job's edit page.",
  ownersAfter: "Owners can be added once the job exists.",
  create: "Create job",
  save: "Save changes",
  cancel: "Cancel",
  created: "Job created.",
  saved: "Job saved.",
  notFound: "This job does not exist or was deleted.",
  destinationsRequired: "Choose at least one destination.",
  databasesRequired: "Select at least one database.",
} as const;

const positiveOrEmpty = z
  .string()
  .trim()
  .refine((v) => v === "" || (/^\d+$/.test(v) && Number(v) >= 1), T.positive);

const schema = z
  .object({
    name: z.string().trim().min(1, "Enter a name.").max(200),
    slug: z
      .string()
      .trim()
      .refine((v) => v === "" || (SLUG_RE.test(v) && v.length <= 63), T.slugHint),
    description: z.string().max(2000),
    connectionId: z.string().min(1, "Choose a connection."),
    dbMode: z.enum(["ALL", "SELECTED"]),
    databases: z.array(z.string()),
    exclude: z.array(z.string()),
    includeGlobals: z.boolean(),
    keepLast: positiveOrEmpty,
    keepDays: positiveOrEmpty,
    timeoutMinutes: positiveOrEmpty,
    verification: z.enum(["STANDARD", "DEEP"]),
    destinationIds: z.array(z.string()).min(1, T.destinationsRequired).max(16),
    enabled: z.boolean(),
    keyChoice: z.enum(["generate-passphrase", "generate-keypair", "passphrase", "recipient"]),
    passphrase: z.string(),
    recipient: z.string(),
    keyLabel: z.string().max(200),
  })
  .superRefine((v, ctx) => {
    if (v.keepLast === "" && v.keepDays === "") {
      ctx.addIssue({ code: "custom", path: ["keepLast"], message: T.retentionRequired });
    }
    if (v.dbMode === "SELECTED" && v.databases.length === 0) {
      ctx.addIssue({ code: "custom", path: ["databases"], message: T.databasesRequired });
    }
    if (v.keyChoice === "passphrase" && Array.from(v.passphrase).length < MIN_JOB_PASSPHRASE) {
      ctx.addIssue({ code: "custom", path: ["passphrase"], message: T.passphraseTooShort });
    }
    if (v.keyChoice === "recipient" && !AGE_RECIPIENT_RE.test(v.recipient.trim())) {
      ctx.addIssue({ code: "custom", path: ["recipient"], message: T.recipientInvalid });
    }
  });
type Values = z.infer<typeof schema>;
const FIELDS = [
  "name",
  "slug",
  "description",
  "connectionId",
  "databases",
  "exclude",
  "keepLast",
  "keepDays",
  "timeoutMinutes",
  "destinationIds",
  "passphrase",
  "recipient",
] as const satisfies readonly (keyof Values)[];
/** Server VALIDATION field names that differ from the form's. */
const FIELD_MAP = {
  timeoutSeconds: "timeoutMinutes",
  retention: "keepLast",
  "retention.keepLast": "keepLast",
  "retention.keepDays": "keepDays",
  "encryption.passphrase": "passphrase",
  "encryption.recipient": "recipient",
} as const;

function defaults(job?: Job): Values {
  return {
    name: job?.name ?? "",
    slug: job?.slug ?? "",
    description: job?.description ?? "",
    connectionId: job?.connection.id ?? "",
    dbMode: job?.dbMode ?? "SELECTED",
    databases: job ? [...job.databases] : [],
    exclude: job ? [...job.exclude] : [],
    includeGlobals: job?.includeGlobals ?? true,
    keepLast: job?.retention.keepLast != null ? String(job.retention.keepLast) : job ? "" : "7",
    keepDays: job?.retention.keepDays != null ? String(job.retention.keepDays) : job ? "" : "30",
    timeoutMinutes: job?.timeoutSeconds != null ? String(Math.ceil(job.timeoutSeconds / 60)) : "",
    verification: job?.verification ?? "STANDARD",
    destinationIds: job
      ? [...job.destinations].sort((a, b) => a.position - b.position).map((d) => d.id)
      : [],
    enabled: job?.enabled ?? true,
    keyChoice: "generate-passphrase",
    passphrase: "",
    recipient: "",
    keyLabel: "",
  };
}

function encryptionInput(v: Values): JobEncryptionInput | undefined {
  switch (v.keyChoice) {
    case "generate-passphrase":
      return undefined;
    case "generate-keypair":
      return { mode: "RECIPIENT", ...(v.keyLabel.trim() ? { label: v.keyLabel.trim() } : {}) };
    case "passphrase":
      return { mode: "PASSPHRASE", passphrase: v.passphrase };
    case "recipient":
      return {
        mode: "RECIPIENT",
        recipient: v.recipient.trim(),
        ...(v.keyLabel.trim() ? { label: v.keyLabel.trim() } : {}),
      };
  }
}

/** /jobs/new */
export function JobCreatePage() {
  return <JobEditor />;
}

/** /jobs/:jobId/edit */
export function JobEditPage() {
  const { jobId = "" } = useParams();
  const { data, loading, error } = useQuery(JobDetailQuery, { variables: { id: jobId } });
  if (loading && !data) {
    return (
      <div className="flex flex-col gap-4">
        <Skeleton className="h-12 w-72" />
        <Skeleton className="h-64 w-full" />
      </div>
    );
  }
  if (error) {
    return (
      <Alert variant="destructive">
        <AlertDescription>{userMessage(error)}</AlertDescription>
      </Alert>
    );
  }
  if (!data?.job) {
    return (
      <Alert>
        <AlertDescription>{T.notFound}</AlertDescription>
      </Alert>
    );
  }
  return <JobEditor key={data.job.id} job={data.job} />;
}

/** A ledger section of the editor: heading column + controls. */
function EditorSection({
  title,
  lead,
  icon,
  children,
}: {
  title: string;
  lead: string;
  icon?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="@container border-b border-rule py-7 last:border-b-0">
      <div className="grid gap-x-10 gap-y-4 @3xl:grid-cols-[minmax(0,16rem)_minmax(0,1fr)]">
        <div className="flex flex-col gap-1.5">
          <h2 className="flex items-center gap-2 text-[0.9375rem] font-semibold tracking-tight">
            {icon}
            {title}
          </h2>
          <p className="text-sm text-muted-foreground">{lead}</p>
        </div>
        <div className="flex min-w-0 flex-col gap-5">{children}</div>
      </div>
    </section>
  );
}

function JobEditor({ job }: { job?: Job }) {
  const flags = useRoleFlags();
  const navigate = useNavigate();
  const { withStepUp } = useStepUp();
  const options = useQuery(JobEditorOptionsQuery);
  const [createJob] = useMutation(CreateJobMutation, {
    // The result may carry a generated key: never cache it.
    fetchPolicy: "no-cache",
    refetchQueries: [JobsListQuery],
  });
  const [updateJob] = useMutation(UpdateJobMutation, {
    refetchQueries: [JobDetailQuery, JobsListQuery],
    awaitRefetchQueries: true,
  });
  const form = useForm<Values>({ resolver: zodResolver(schema), defaultValues: defaults(job) });
  const initialDraft = job ? draftFromSchedule(job.schedule) : defaultDraft();
  const [draft, setDraft] = useState<ScheduleDraft>(initialDraft);
  const [scheduleProblem, setScheduleProblem] = useState<string | null>(null);
  const [scheduleServerError, setScheduleServerError] = useState<string | undefined>();
  const [shownKey, setShownKey] = useState<{ key: OneTimeKey; jobId: string } | null>(null);
  const onValidity = useCallback((p: string | null) => {
    setScheduleProblem(p);
  }, []);

  const connectionId = useWatch({ control: form.control, name: "connectionId" });
  const dbMode = useWatch({ control: form.control, name: "dbMode" });
  const keyChoice = useWatch({ control: form.control, name: "keyChoice" });
  const connections = (options.data?.connections ?? []).filter((c) => !c.temporary);
  const engine =
    connections.find((c) => c.id === connectionId)?.engine ??
    (job?.connection.id === connectionId ? job.connection.engine : undefined);

  const onSubmit = async (v: Values) => {
    if (scheduleProblem) return;
    setScheduleServerError(undefined);
    const retention = {
      ...(v.keepLast ? { keepLast: Number(v.keepLast) } : {}),
      ...(v.keepDays ? { keepDays: Number(v.keepDays) } : {}),
    };
    const schedule = draftToInput(draft);
    try {
      if (!job) {
        const input: CreateJobInput = {
          name: v.name.trim(),
          description: v.description,
          connectionId: v.connectionId,
          dbMode: v.dbMode,
          ...(v.dbMode === "SELECTED" ? { databases: v.databases } : { exclude: v.exclude }),
          includeGlobals: engine === "POSTGRES" ? v.includeGlobals : false,
          schedule,
          enabled: v.enabled,
          retention,
          verification: v.verification,
          destinationIds: v.destinationIds,
          ...(v.slug.trim() ? { slug: v.slug.trim() } : {}),
          ...(v.timeoutMinutes ? { timeoutSeconds: Number(v.timeoutMinutes) * 60 } : {}),
        };
        const enc = encryptionInput(v);
        if (enc) input.encryption = enc;
        const r = await withStepUp(() => createJob({ variables: { input } }));
        const result = r.data?.createJob;
        if (!result) return;
        toast.success(T.created);
        if (result.generatedPassphrase) {
          setShownKey({
            key: passphraseKey(result.job.slug, result.generatedPassphrase, 1),
            jobId: result.job.id,
          });
        } else if (result.generatedKeypair) {
          setShownKey({ key: identityKey(result.generatedKeypair), jobId: result.job.id });
        } else {
          void navigate(`/jobs/${result.job.id}`);
        }
        return;
      }
      const input: UpdateJobInput = {
        name: v.name.trim(),
        description: v.description,
        connectionId: v.connectionId,
        dbMode: v.dbMode,
        databases: v.dbMode === "SELECTED" ? v.databases : [],
        exclude: v.dbMode === "ALL" ? v.exclude : [],
        includeGlobals: engine === "POSTGRES" ? v.includeGlobals : false,
        retention,
        verification: v.verification,
        destinationIds: v.destinationIds,
        ...(v.timeoutMinutes
          ? { timeoutSeconds: Number(v.timeoutMinutes) * 60 }
          : job.timeoutSeconds != null
            ? { clearTimeout: true }
            : {}),
      };
      // Only a changed schedule is sent: the server re-plans it from now.
      if (JSON.stringify(draftToInput(initialDraft)) !== JSON.stringify(schedule)) {
        input.schedule = schedule;
      }
      await updateJob({ variables: { id: job.id, input } });
      toast.success(T.saved);
      void navigate(`/jobs/${job.id}`);
    } catch (err) {
      const scheduleErr = apiErrors(err).find(
        (e) => e.code === "VALIDATION" && e.field?.startsWith("schedule"),
      );
      if (scheduleErr) setScheduleServerError(userMessage(err));
      if (!applyFieldErrors(err, form.setError, FIELDS, FIELD_MAP) && !scheduleErr) {
        reportActionError(err);
      }
    }
  };

  const submitting = form.formState.isSubmitting;
  const backTo = job ? `/jobs/${job.id}` : "/jobs";

  return (
    <div className="flex flex-col gap-6">
      <Link
        to={backTo}
        className="inline-flex w-fit items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft aria-hidden="true" className="size-3.5" /> {job ? T.backJob : T.back}
      </Link>
      <PageHeader
        eyebrow={job ? T.editEyebrow : T.newEyebrow}
        title={job ? job.name : T.newTitle}
      />
      {options.error ? (
        <Alert variant="destructive">
          <AlertDescription>{userMessage(options.error)}</AlertDescription>
        </Alert>
      ) : null}

      <Form {...form}>
        <form
          onSubmit={(e) => void form.handleSubmit(onSubmit)(e)}
          noValidate
          className="flex flex-col"
        >
          <EditorSection title={T.sections.basics} lead={T.leads.basics}>
            <div className="grid gap-5 sm:grid-cols-2">
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
              <FormField
                control={form.control}
                name="slug"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{T.slug}</FormLabel>
                    <FormControl>
                      <Input
                        autoComplete="off"
                        spellCheck={false}
                        readOnly={Boolean(job)}
                        className="font-mono"
                        {...field}
                      />
                    </FormControl>
                    <FormDescription>{job ? T.slugFixed : T.slugHint}</FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>
            <FormField
              control={form.control}
              name="description"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{T.description}</FormLabel>
                  <FormControl>
                    <textarea
                      rows={2}
                      className="w-full rounded-md border border-input bg-surface px-3 py-2 text-sm shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 dark:bg-surface-sunken"
                      {...field}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          </EditorSection>

          <EditorSection title={T.sections.source} lead={T.leads.source}>
            <FormField
              control={form.control}
              name="connectionId"
              render={({ field }) => (
                <FormItem className="sm:max-w-md">
                  <FormLabel>{T.connection}</FormLabel>
                  <FormControl>
                    <NativeSelect {...field}>
                      <option value="">{T.chooseConnection}</option>
                      {connections.map((c) => (
                        <option key={c.id} value={c.id}>
                          {c.name} ({c.engine.toLowerCase()} {c.versionKey})
                        </option>
                      ))}
                      {job && !connections.some((c) => c.id === job.connection.id) ? (
                        <option value={job.connection.id}>{job.connection.name}</option>
                      ) : null}
                    </NativeSelect>
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name="dbMode"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{T.dbMode}</FormLabel>
                  <div
                    role="radiogroup"
                    aria-label={T.dbMode}
                    className="flex flex-col gap-1.5 sm:flex-row sm:gap-6"
                  >
                    {(["SELECTED", "ALL"] as const).map((m) => (
                      <label key={m} className="flex items-center gap-2 text-sm">
                        <input
                          type="radio"
                          name={field.name}
                          value={m}
                          checked={field.value === m}
                          onChange={() => {
                            field.onChange(m);
                          }}
                          className="size-4 accent-primary"
                        />
                        {m === "ALL" ? T.dbModeAll : T.dbModeSelected}
                      </label>
                    ))}
                  </div>
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name={dbMode === "ALL" ? "exclude" : "databases"}
              render={({ field }) => (
                <FormItem>
                  <DatabasePicker
                    connectionId={connectionId}
                    mode={dbMode}
                    value={field.value}
                    onChange={field.onChange}
                  />
                  <FormMessage />
                </FormItem>
              )}
            />
            {engine === "POSTGRES" || engine === undefined ? (
              <FormField
                control={form.control}
                name="includeGlobals"
                render={({ field }) => (
                  <CheckboxField
                    label={T.includeGlobals}
                    hint={T.includeGlobalsHint}
                    checked={field.value}
                    disabled={engine !== "POSTGRES"}
                    onChange={(e) => {
                      field.onChange(e.target.checked);
                    }}
                  />
                )}
              />
            ) : null}
          </EditorSection>

          <EditorSection title={T.sections.schedule} lead={T.leads.schedule}>
            <ScheduleBuilder
              value={draft}
              onChange={(d) => {
                setDraft(d);
                setScheduleServerError(undefined);
              }}
              error={scheduleServerError}
              onValidity={onValidity}
            />
            {!job ? (
              <FormField
                control={form.control}
                name="enabled"
                render={({ field }) => (
                  <CheckboxField
                    label={T.enabled}
                    checked={field.value}
                    onChange={(e) => {
                      field.onChange(e.target.checked);
                    }}
                  />
                )}
              />
            ) : null}
          </EditorSection>

          <EditorSection title={T.sections.retention} lead={T.leads.retention}>
            <div className="grid gap-5 sm:grid-cols-3">
              <FormField
                control={form.control}
                name="keepLast"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{T.keepLast}</FormLabel>
                    <FormControl>
                      <Input inputMode="numeric" className="font-mono" {...field} />
                    </FormControl>
                    <FormDescription>{T.keepLastHint}</FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name="keepDays"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{T.keepDays}</FormLabel>
                    <FormControl>
                      <Input inputMode="numeric" className="font-mono" {...field} />
                    </FormControl>
                    <FormDescription>{T.keepDaysHint}</FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name="timeoutMinutes"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{T.timeout}</FormLabel>
                    <FormControl>
                      <Input inputMode="numeric" className="font-mono" {...field} />
                    </FormControl>
                    <FormDescription>{T.timeoutHint}</FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>
            <FormField
              control={form.control}
              name="verification"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{T.verification}</FormLabel>
                  <div
                    role="radiogroup"
                    aria-label={T.verification}
                    className="flex flex-col gap-1.5"
                  >
                    {(["STANDARD", "DEEP"] as const).map((m) => (
                      <label key={m} className="flex items-center gap-2 text-sm">
                        <input
                          type="radio"
                          name={field.name}
                          value={m}
                          checked={field.value === m}
                          onChange={() => {
                            field.onChange(m);
                          }}
                          className="size-4 accent-primary"
                        />
                        {m === "STANDARD" ? T.verificationStandard : T.verificationDeep}
                      </label>
                    ))}
                  </div>
                  <p className="text-xs text-muted-foreground">{T.verificationHint}</p>
                </FormItem>
              )}
            />
          </EditorSection>

          <EditorSection title={T.sections.destinations} lead={T.leads.destinations}>
            <FormField
              control={form.control}
              name="destinationIds"
              render={({ field, fieldState }) => (
                <FormItem>
                  <DestinationPicker
                    options={options.data?.destinations ?? []}
                    value={field.value}
                    onChange={field.onChange}
                    invalid={Boolean(fieldState.error)}
                  />
                  <FormMessage />
                </FormItem>
              )}
            />
          </EditorSection>

          {!job ? (
            <EditorSection
              title={T.sections.encryption}
              lead={T.leads.encryption}
              icon={<KeyRound aria-hidden="true" className="size-4 text-muted-foreground" />}
            >
              {flags.admin ? (
                <>
                  <FormField
                    control={form.control}
                    name="keyChoice"
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{T.keyChoice}</FormLabel>
                        <div
                          role="radiogroup"
                          aria-label={T.keyChoice}
                          className="flex flex-col gap-1.5"
                        >
                          {(
                            [
                              ["generate-passphrase", T.keyGeneratePassphrase],
                              ["generate-keypair", T.keyGenerateKeypair],
                              ["passphrase", T.keyPassphrase],
                              ["recipient", T.keyRecipient],
                            ] as const
                          ).map(([value, label]) => (
                            <label key={value} className="flex items-center gap-2 text-sm">
                              <input
                                type="radio"
                                name={field.name}
                                value={value}
                                checked={field.value === value}
                                onChange={() => {
                                  field.onChange(value);
                                }}
                                className="size-4 accent-primary"
                              />
                              {label}
                            </label>
                          ))}
                        </div>
                        <p className="text-xs text-muted-foreground">{T.keyAdminNote}</p>
                      </FormItem>
                    )}
                  />
                  {keyChoice === "passphrase" ? (
                    <FormField
                      control={form.control}
                      name="passphrase"
                      render={({ field }) => (
                        <FormItem className="sm:max-w-md">
                          <FormLabel>{T.passphrase}</FormLabel>
                          <FormControl>
                            <Input type="password" autoComplete="new-password" {...field} />
                          </FormControl>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  ) : null}
                  {keyChoice === "recipient" ? (
                    <FormField
                      control={form.control}
                      name="recipient"
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{T.recipient}</FormLabel>
                          <FormControl>
                            <Input
                              spellCheck={false}
                              autoComplete="off"
                              className="font-mono text-xs"
                              {...field}
                            />
                          </FormControl>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  ) : null}
                  {keyChoice === "recipient" || keyChoice === "generate-keypair" ? (
                    <FormField
                      control={form.control}
                      name="keyLabel"
                      render={({ field }) => (
                        <FormItem className="sm:max-w-md">
                          <FormLabel>{T.keyLabel}</FormLabel>
                          <FormControl>
                            <Input autoComplete="off" {...field} />
                          </FormControl>
                        </FormItem>
                      )}
                    />
                  ) : null}
                </>
              ) : (
                <p className="flex items-start gap-2 text-sm text-muted-foreground">
                  <ShieldCheck
                    aria-hidden="true"
                    className="mt-0.5 size-4 shrink-0 text-status-success"
                  />
                  {T.keyDefaultNote}
                </p>
              )}
              <p className="text-xs text-muted-foreground">{T.encryptionAfter}</p>
            </EditorSection>
          ) : null}

          <div className="sticky bottom-0 z-10 -mx-1 flex justify-end gap-2 border-t border-rule bg-background/95 px-1 py-4 backdrop-blur">
            <Button type="button" variant="outline" asChild>
              <Link to={backTo}>{T.cancel}</Link>
            </Button>
            <Button type="submit" disabled={submitting || scheduleProblem !== null}>
              {submitting ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
              {job ? T.save : T.create}
            </Button>
          </div>
        </form>
      </Form>

      {job ? (
        <div className={cn("flex flex-col rounded-lg border bg-card px-6")}>
          <EditorSection
            title={T.sections.encryption}
            lead={T.leads.encryption}
            icon={<KeyRound aria-hidden="true" className="size-4 text-muted-foreground" />}
          >
            <EncryptionPanel job={job} admin={flags.admin} />
          </EditorSection>
          <EditorSection
            title={T.sections.owners}
            lead={T.leads.owners}
            icon={<Users aria-hidden="true" className="size-4 text-muted-foreground" />}
          >
            <OwnersPanel job={job} admin={flags.admin} />
          </EditorSection>
          <EditorSection
            title={T.sections.testRestore}
            lead={T.leads.testRestore}
            icon={<FlaskConical aria-hidden="true" className="size-4 text-muted-foreground" />}
          >
            <TestRestoreSettings job={job} admin={flags.admin} />
          </EditorSection>
          <EditorSection
            title={T.sections.notifications}
            lead={T.leads.notifications}
            icon={<BellRing aria-hidden="true" className="size-4 text-muted-foreground" />}
          >
            <JobNotificationsPanel job={job} admin={flags.admin} />
          </EditorSection>
        </div>
      ) : (
        <p className="text-xs text-muted-foreground">{T.ownersAfter}</p>
      )}

      <OneTimeKeyDialog
        value={shownKey?.key ?? null}
        onDone={() => {
          const id = shownKey?.jobId;
          setShownKey(null);
          if (id) void navigate(`/jobs/${id}`);
        }}
      />
    </div>
  );
}
