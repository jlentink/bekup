import { useMutation, useQuery } from "@apollo/client/react";
import {
  BellRing,
  Link2,
  Loader2,
  Mail,
  MoreHorizontal,
  Pencil,
  Plus,
  Power,
  PowerOff,
  RefreshCcw,
  Send,
  Trash2,
  Unlink,
  Webhook,
} from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { Tag } from "@/components/status/tag";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Skeleton } from "@/components/ui/skeleton";
import { reportActionError, useStepUp } from "@/features/auth/use-step-up";
import { SigningKeyDialog, type IssuedKey } from "@/features/notifications/signing-key-dialog";
import { getFragmentData } from "@/graphql/gen";
import type { SharedChannelFieldsFragment } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { formatDateTime, formatRelative } from "@/lib/format";
import { messages } from "@/lib/messages";
import { cn } from "@/lib/utils";

import {
  AttachSharedChannelMutation,
  CreateSharedChannelMutation,
  DeleteSharedChannelMutation,
  DetachSharedChannelMutation,
  DisableSharedChannelMutation,
  EnableSharedChannelMutation,
  NotificationCatalogQuery,
  RotateSharedChannelKeyMutation,
  SharedChannelsQuery,
  TestSharedChannelMutation,
  UpdateSharedChannelMutation,
  SharedChannelFields,
} from "../operations";
import { docs } from "../docs";
import { DocLink } from "../ui";
import { AttachDialog, ChannelFormDialog, type ChannelDraft } from "./dialogs";
import { describeFilter, toFilterInput, type CatalogGroup, type FilterDraft } from "./filter";

type Channel = SharedChannelFieldsFragment;

const T = {
  eyebrow: "Administration · Notifications",
  title: "Shared channels",
  lead: "Team e-mail lists and webhooks that receive the events of the jobs they are attached to. Security alerts never go to shared channels: they reach every admin personally.",
  create: "New channel",
  guide: "Shared channels guide",
  emptyTitle: "No shared channels yet",
  emptyLead:
    "Create an e-mail list for the on-call team or a webhook for your chat tool, then attach it to jobs.",
  webhook: "Webhook",
  emailList: "E-mail list",
  enabled: "Enabled",
  disabled: "Disabled",
  autoDisabled: "Auto-disabled",
  failures: (n: number) => `${String(n)} failed deliver${n === 1 ? "y" : "ies"} in a row`,
  lastFailure: (code: string, when: string) => `Last failure: ${code}, ${when}`,
  attachments: "Attached jobs",
  noAttachments: "Not attached to a job: it receives nothing yet.",
  attach: "Attach to a job",
  editFilter: (job: string) => `Change the events from ${job}`,
  detach: (job: string) => `Detach from ${job}`,
  actions: (name: string) => `Actions for ${name}`,
  test: "Send test",
  testing: "Sending…",
  edit: "Edit",
  rotate: "Rotate signing key",
  enable: "Enable",
  disable: "Disable",
  delete: "Delete",
  created: "Channel created.",
  saved: "Channel saved.",
  deletedToast: "Channel deleted.",
  enabledToast: "Channel enabled.",
  disabledToast: "Channel disabled.",
  attached: "Channel attached.",
  filterSaved: "Event filter saved.",
  detached: "Channel detached.",
  testOk: (status?: number | null) =>
    status ? `Test delivered (HTTP ${String(status)}).` : "Test delivered.",
  testFailed: (msg: string) => `Test failed: ${msg}`,
  deleteTitle: (name: string) => `Delete ${name}?`,
  deleteLead:
    "The channel and its job attachments are removed. Receivers stop getting events at once. This cannot be undone.",
  rotateTitle: "Rotate the signing key?",
  rotateLead:
    "The old key stops working at once: receivers reject bekup's requests until you store the new key, which is shown only once.",
  detachTitle: (job: string) => `Detach from ${job}?`,
  detachLead: "The channel stops receiving events of this job.",
} as const;

type Pending =
  | { kind: "create" }
  | { kind: "edit"; channel: Channel }
  | { kind: "attach"; channel: Channel }
  | { kind: "filter"; channel: Channel; job: { id: string; name: string }; initial: FilterDraft }
  | { kind: "delete"; channel: Channel }
  | { kind: "rotate"; channel: Channel }
  | { kind: "detach"; channel: Channel; job: { id: string; name: string } };

/** Shared channels (admin, D-177/D-191). */
export function SharedChannelsPage() {
  const { data, error, refetch } = useQuery(SharedChannelsQuery, {
    fetchPolicy: "cache-and-network",
  });
  const { data: catalogData } = useQuery(NotificationCatalogQuery);
  const catalog: readonly CatalogGroup[] = catalogData?.notificationCatalog ?? [];
  const channels = data ? getFragmentData(SharedChannelFields, data.sharedChannels) : undefined;

  const { withStepUp } = useStepUp();
  const refetchList = { refetchQueries: [SharedChannelsQuery] };
  // Signing keys never enter the Apollo cache (D-193, D-194).
  const oneTime = { ...refetchList, fetchPolicy: "no-cache" as const };
  const [createChannel] = useMutation(CreateSharedChannelMutation, oneTime);
  const [updateChannel] = useMutation(UpdateSharedChannelMutation);
  const [rotateKey] = useMutation(RotateSharedChannelKeyMutation, oneTime);
  const [deleteChannel] = useMutation(DeleteSharedChannelMutation, refetchList);
  const [enable] = useMutation(EnableSharedChannelMutation);
  const [disable] = useMutation(DisableSharedChannelMutation);
  const [attach] = useMutation(AttachSharedChannelMutation);
  const [detach] = useMutation(DetachSharedChannelMutation);

  const [pending, setPending] = useState<Pending | null>(null);
  const [key, setKey] = useState<IssuedKey | null>(null);
  const close = () => {
    setPending(null);
  };

  const onCreate = async (d: ChannelDraft) => {
    const r = await withStepUp(() =>
      createChannel({
        variables: {
          input:
            d.type === "WEBHOOK"
              ? { name: d.name, type: d.type, url: d.url }
              : { name: d.name, type: d.type, addresses: d.addresses },
        },
      }),
    );
    toast.success(T.created);
    const k = r.data?.createSharedChannel.signingKey;
    if (k) setKey({ key: k, label: d.name, kind: "added" });
  };

  const onEdit = async (c: Channel, d: ChannelDraft) => {
    await withStepUp(() =>
      updateChannel({
        variables: {
          id: c.id,
          input:
            c.type === "WEBHOOK"
              ? { name: d.name, ...(d.url ? { url: d.url } : {}) }
              : { name: d.name, addresses: d.addresses },
        },
      }),
    );
    toast.success(T.saved);
  };

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        eyebrow={T.eyebrow}
        title={T.title}
        description={T.lead}
        actions={
          <>
            <DocLink href={docs.sharedChannels}>{T.guide}</DocLink>
            <Button
              onClick={() => {
                setPending({ kind: "create" });
              }}
            >
              <Plus aria-hidden="true" /> {T.create}
            </Button>
          </>
        }
      />
      {error && !channels ? (
        <Alert variant="destructive">
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
      ) : !channels ? (
        <div className="grid gap-4" aria-busy="true">
          <Skeleton className="h-40 w-full" />
          <Skeleton className="h-40 w-full" />
        </div>
      ) : channels.length === 0 ? (
        <EmptyState icon={BellRing} title={T.emptyTitle}>
          {T.emptyLead}
        </EmptyState>
      ) : (
        <ul className="grid gap-4 xl:grid-cols-2">
          {channels.map((c) => (
            <li key={c.id}>
              <ChannelCard
                channel={c}
                catalog={catalog}
                onPending={setPending}
                onToggle={async () => {
                  try {
                    if (c.state.enabled) {
                      await disable({ variables: { id: c.id } });
                      toast.success(T.disabledToast);
                    } else {
                      await enable({ variables: { id: c.id } });
                      toast.success(T.enabledToast);
                    }
                  } catch (err) {
                    reportActionError(err);
                  }
                }}
              />
            </li>
          ))}
        </ul>
      )}

      {pending?.kind === "create" ? (
        <ChannelFormDialog open onOpenChange={close} onSubmit={onCreate} />
      ) : null}
      {pending?.kind === "edit" ? (
        <ChannelFormDialog
          key={pending.channel.id}
          open
          onOpenChange={close}
          initial={pending.channel}
          displayUrl={pending.channel.displayUrl}
          onSubmit={(d) => onEdit(pending.channel, d)}
        />
      ) : null}
      {pending?.kind === "attach" || pending?.kind === "filter" ? (
        <AttachDialog
          key={`${pending.channel.id}-${pending.kind === "filter" ? pending.job.id : "new"}`}
          open
          onOpenChange={close}
          catalog={catalog}
          fixedJob={pending.kind === "filter" ? pending.job : undefined}
          initial={pending.kind === "filter" ? pending.initial : undefined}
          attachedJobIds={pending.channel.attachments.map((a) => a.jobId)}
          onSubmit={async (jobId, f) => {
            await attach({
              variables: {
                jobId,
                channelId: pending.channel.id,
                filter: toFilterInput(f, catalog),
              },
            });
            toast.success(pending.kind === "filter" ? T.filterSaved : T.attached);
          }}
        />
      ) : null}
      <ConfirmDialog
        open={pending?.kind === "delete"}
        onOpenChange={(o) => {
          if (!o) close();
        }}
        title={pending?.kind === "delete" ? T.deleteTitle(pending.channel.name) : ""}
        description={T.deleteLead}
        confirmLabel={T.delete}
        destructive
        onConfirm={async () => {
          if (pending?.kind !== "delete") return;
          try {
            await withStepUp(() => deleteChannel({ variables: { id: pending.channel.id } }));
            toast.success(T.deletedToast);
          } catch (err) {
            reportActionError(err);
            throw err;
          }
        }}
      />
      <ConfirmDialog
        open={pending?.kind === "rotate"}
        onOpenChange={(o) => {
          if (!o) close();
        }}
        title={T.rotateTitle}
        description={T.rotateLead}
        confirmLabel={T.rotate}
        destructive
        onConfirm={async () => {
          if (pending?.kind !== "rotate") return;
          try {
            const r = await withStepUp(() => rotateKey({ variables: { id: pending.channel.id } }));
            const k = r.data?.rotateSharedChannelKey.signingKey;
            if (k) setKey({ key: k, label: pending.channel.name, kind: "rotated" });
          } catch (err) {
            reportActionError(err);
            throw err;
          }
        }}
      />
      <ConfirmDialog
        open={pending?.kind === "detach"}
        onOpenChange={(o) => {
          if (!o) close();
        }}
        title={pending?.kind === "detach" ? T.detachTitle(pending.job.name) : ""}
        description={T.detachLead}
        confirmLabel="Detach"
        onConfirm={async () => {
          if (pending?.kind !== "detach") return;
          try {
            await detach({ variables: { jobId: pending.job.id, channelId: pending.channel.id } });
            toast.success(T.detached);
          } catch (err) {
            reportActionError(err);
            throw err;
          }
        }}
      />
      <SigningKeyDialog
        issued={key}
        onDone={() => {
          setKey(null);
        }}
      />
    </div>
  );
}

function ChannelCard({
  channel: c,
  catalog,
  onPending,
  onToggle,
}: {
  channel: Channel;
  catalog: readonly CatalogGroup[];
  onPending: (p: Pending) => void;
  onToggle: () => Promise<void>;
}) {
  const [test, { loading: testing }] = useMutation(TestSharedChannelMutation, {
    fetchPolicy: "no-cache",
  });
  const [toggling, setToggling] = useState(false);
  const webhook = c.type === "WEBHOOK";
  const auto =
    !c.state.enabled && c.state.disabledReason != null && c.state.disabledReason !== "manual";
  const Icon = webhook ? Webhook : Mail;

  return (
    <article
      aria-label={c.name}
      data-testid="shared-channel"
      className={cn(
        "flex h-full flex-col rounded-lg border bg-card shadow-xs",
        !c.state.enabled && "bg-surface-sunken",
      )}
    >
      <header className="flex items-start gap-3 border-b border-rule p-4">
        <span
          aria-hidden="true"
          className="inline-flex size-9 shrink-0 items-center justify-center rounded-md border bg-surface text-muted-foreground"
        >
          <Icon className="size-4" />
        </span>
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="truncate text-[0.9375rem] font-semibold">{c.name}</h2>
            <span className="eyebrow">{webhook ? T.webhook : T.emailList}</span>
          </div>
          {webhook ? (
            <p
              className="truncate font-mono text-xs text-muted-foreground"
              title={c.displayUrl ?? ""}
            >
              {c.displayUrl}
              <span aria-hidden="true">/…</span>
            </p>
          ) : (
            <p
              className="truncate font-mono text-xs text-muted-foreground"
              title={c.addresses.join(", ")}
            >
              {c.addresses.join(", ")}
            </p>
          )}
        </div>
        <div className="flex shrink-0 items-center gap-1">
          {c.state.enabled ? (
            <Tag tone="success">{T.enabled}</Tag>
          ) : auto ? (
            <Tag tone="danger" title={c.state.disabledMessage ?? undefined}>
              {T.autoDisabled}
            </Tag>
          ) : (
            <Tag tone="neutral">{T.disabled}</Tag>
          )}
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="icon" aria-label={T.actions(c.name)}>
                <MoreHorizontal aria-hidden="true" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem
                onSelect={() => {
                  onPending({ kind: "edit", channel: c });
                }}
              >
                <Pencil aria-hidden="true" /> {T.edit}
              </DropdownMenuItem>
              {webhook ? (
                <DropdownMenuItem
                  onSelect={() => {
                    onPending({ kind: "rotate", channel: c });
                  }}
                >
                  <RefreshCcw aria-hidden="true" /> {T.rotate}
                </DropdownMenuItem>
              ) : null}
              <DropdownMenuSeparator />
              <DropdownMenuItem
                variant="destructive"
                onSelect={() => {
                  onPending({ kind: "delete", channel: c });
                }}
              >
                <Trash2 aria-hidden="true" /> {T.delete}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </header>

      {!c.state.enabled && c.state.disabledMessage ? (
        <p className="border-b border-rule px-4 py-2.5 text-xs text-muted-foreground">
          {c.state.disabledMessage}
        </p>
      ) : null}
      {c.state.consecutiveFailures > 0 || c.state.lastFailureAt ? (
        <p className="flex flex-wrap gap-x-3 border-b border-rule px-4 py-2.5 text-xs text-status-failed-fg">
          {c.state.consecutiveFailures > 0 ? (
            <span>{T.failures(c.state.consecutiveFailures)}</span>
          ) : null}
          {c.state.lastFailureAt ? (
            <span title={formatDateTime(c.state.lastFailureAt)}>
              {T.lastFailure(
                c.state.lastFailureCode ?? "error",
                formatRelative(c.state.lastFailureAt),
              )}
            </span>
          ) : null}
        </p>
      ) : null}

      <div className="flex flex-1 flex-col gap-2 p-4">
        <p className="eyebrow">{T.attachments}</p>
        {c.attachments.length === 0 ? (
          <p className="text-sm text-muted-foreground">{T.noAttachments}</p>
        ) : (
          <ul className="flex flex-col divide-y divide-rule rounded-md border">
            {c.attachments.map((a) => (
              <li key={a.jobId} className="flex items-center gap-3 px-3 py-2">
                <Link2 aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
                <div className="flex min-w-0 flex-1 flex-col">
                  <span className="truncate text-sm font-medium">{a.jobName}</span>
                  <span className="truncate text-xs text-muted-foreground">
                    {describeFilter(a, catalog)}
                  </span>
                </div>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={T.editFilter(a.jobName)}
                  onClick={() => {
                    onPending({
                      kind: "filter",
                      channel: c,
                      job: { id: a.jobId, name: a.jobName },
                      initial: { groups: a.groups, events: a.events },
                    });
                  }}
                >
                  <Pencil aria-hidden="true" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={T.detach(a.jobName)}
                  onClick={() => {
                    onPending({
                      kind: "detach",
                      channel: c,
                      job: { id: a.jobId, name: a.jobName },
                    });
                  }}
                >
                  <Unlink aria-hidden="true" />
                </Button>
              </li>
            ))}
          </ul>
        )}
      </div>

      <footer className="flex flex-wrap items-center gap-2 border-t border-rule px-4 py-3">
        <Button
          variant="outline"
          size="sm"
          onClick={() => {
            onPending({ kind: "attach", channel: c });
          }}
        >
          <Plus aria-hidden="true" /> {T.attach}
        </Button>
        <Button
          variant="outline"
          size="sm"
          disabled={testing || !c.state.enabled}
          onClick={() => {
            test({ variables: { id: c.id } })
              .then((r) => {
                const res = r.data?.testSharedChannel;
                if (!res) return;
                if (res.ok) toast.success(T.testOk(res.statusCode));
                else toast.error(T.testFailed(res.message ?? res.code ?? "unknown error"));
              })
              .catch((err: unknown) => {
                reportActionError(err);
              });
          }}
        >
          {testing ? (
            <Loader2 aria-hidden="true" className="animate-spin" />
          ) : (
            <Send aria-hidden="true" />
          )}
          {testing ? T.testing : T.test}
        </Button>
        <Button
          variant="ghost"
          size="sm"
          className="ml-auto"
          disabled={toggling}
          onClick={() => {
            setToggling(true);
            void onToggle().finally(() => {
              setToggling(false);
            });
          }}
        >
          {c.state.enabled ? <PowerOff aria-hidden="true" /> : <Power aria-hidden="true" />}
          {c.state.enabled ? T.disable : T.enable}
        </Button>
      </footer>
    </article>
  );
}
