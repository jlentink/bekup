import { useMutation, useQuery } from "@apollo/client/react";
import { BellRing, Link2, Mail, Pencil, Plus, Unlink, Webhook } from "lucide-react";
import { useId, useState } from "react";
import { Link } from "react-router";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { Tag } from "@/components/status/tag";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { AttachDialog } from "@/features/admin/channels/dialogs";
import {
  describeFilter,
  toFilterInput,
  type CatalogGroup,
  type FilterDraft,
} from "@/features/admin/channels/filter";
import {
  AttachSharedChannelMutation,
  DetachSharedChannelMutation,
  NotificationCatalogQuery,
  SharedChannelsQuery,
  SharedChannelFields,
} from "@/features/admin/operations";
import { reportActionError } from "@/features/auth/use-step-up";
import { getFragmentData } from "@/graphql/gen";
import type { JobSharedChannelAttachmentsQuery as AttachmentsResult } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";

import { NativeSelect } from "./fields";
import { JobSharedChannelAttachmentsQuery } from "./operations";
import { personalOverridesPath } from "./notifications-panel-utils";

type Attachment = NonNullable<
  NonNullable<AttachmentsResult["job"]>["sharedChannelAttachments"]
>[number];

const T = {
  attached: "Shared channels for this job",
  none: "No shared channel receives this job's events.",
  noChannels: "No shared channels exist yet.",
  manage: "Manage shared channels",
  pick: "Shared channel",
  pickPlaceholder: "Choose a channel",
  attach: "Attach",
  allAttached: "Every shared channel is attached.",
  disabled: "disabled",
  emailList: "E-mail list",
  editFilter: (name: string) => `Change the events ${name} receives`,
  detach: (name: string) => `Detach ${name}`,
  detachTitle: (name: string) => `Detach ${name} from this job?`,
  detachLead: "The channel stops receiving this job's events at once.",
  detachConfirm: "Detach",
  attachedToast: "Channel attached.",
  filterSaved: "Event filter saved.",
  detachedToast: "Channel detached.",
  staffNote:
    "Shared channels (team e-mail lists and webhooks) are attached by administrators. Ask an admin to change which channels receive this job's events.",
  personal: "Your own notifications for this job",
  personalLead: "Per-job overrides of your personal preferences: e-mail, webhooks and the inbox.",
} as const;

/**
 * Notifications of a job in the job editor (staff). Admins and maintainers
 * see the shared channels attached to the job (Job.sharedChannelAttachments,
 * D-198: masked webhook URL, event filter, enabled state); admins also
 * attach, re-filter or detach them (attachSharedChannel /
 * detachSharedChannel, admin only; security alerts are never offered:
 * attachableCatalog). Backup owners get null from the API and see no list.
 * Everyone gets a link to their own per-job preference overrides.
 */
export function JobNotificationsPanel({
  job,
  admin,
}: {
  job: { id: string; name: string };
  admin: boolean;
}) {
  return (
    <div className="flex flex-col gap-5">
      <SharedChannelsForJob job={job} admin={admin} />
      <div className="flex flex-col gap-1 border-t border-dashed border-rule pt-4">
        <Link
          to={personalOverridesPath(job.id)}
          state={{ jobName: job.name }}
          className="inline-flex items-center gap-1.5 self-start text-sm font-medium text-primary underline-offset-4 hover:underline"
        >
          <BellRing aria-hidden="true" className="size-3.5" />
          {T.personal}
        </Link>
        <p className="text-xs text-muted-foreground">{T.personalLead}</p>
      </div>
    </div>
  );
}

interface Target {
  id: string;
  name: string;
}

type Pending =
  | { kind: "attach"; channel: Target }
  | { kind: "filter"; channel: Target; initial: FilterDraft }
  | { kind: "detach"; channel: Target };

function SharedChannelsForJob({
  job,
  admin,
}: {
  job: { id: string; name: string };
  admin: boolean;
}) {
  const listId = useId();
  const { data, loading, error, refetch } = useQuery(JobSharedChannelAttachmentsQuery, {
    variables: { id: job.id },
    fetchPolicy: "cache-and-network",
  });
  const { data: catalogData } = useQuery(NotificationCatalogQuery);
  const catalog: readonly CatalogGroup[] = catalogData?.notificationCatalog ?? [];
  const [pending, setPending] = useState<Pending | null>(null);
  const [detach] = useMutation(DetachSharedChannelMutation);
  const [attach] = useMutation(AttachSharedChannelMutation);
  const reload = () => refetch().catch(() => undefined);

  if (loading && !data) return <Skeleton className="h-24" />;
  if (error && !data) {
    return <p className="text-sm text-destructive">{userMessage(error)}</p>;
  }
  const attachments = data?.job?.sharedChannelAttachments;
  // Backup owners get null: shared channels are staff information.
  if (!attachments) return null;

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-col gap-2">
        <p id={listId} className="eyebrow">
          {T.attached}
        </p>
        {attachments.length === 0 ? (
          <p className="text-sm text-muted-foreground">{T.none}</p>
        ) : (
          <ul
            aria-labelledby={listId}
            className="flex flex-col divide-y divide-rule rounded-md border"
          >
            {attachments.map((a) => (
              <AttachmentRow
                key={a.channelId}
                attachment={a}
                catalog={catalog}
                admin={admin}
                onEdit={() => {
                  setPending({
                    kind: "filter",
                    channel: { id: a.channelId, name: a.name },
                    initial: { groups: a.groups, events: a.events },
                  });
                }}
                onDetach={() => {
                  setPending({ kind: "detach", channel: { id: a.channelId, name: a.name } });
                }}
              />
            ))}
          </ul>
        )}
      </div>

      {admin ? (
        <>
          <AttachForm
            attachedIds={attachments.map((a) => a.channelId)}
            onPick={(channel) => {
              setPending({ kind: "attach", channel });
            }}
          />
          <Link
            to="/admin/channels"
            className="inline-flex items-center gap-1.5 self-start text-xs text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
          >
            <Link2 aria-hidden="true" className="size-3" />
            {T.manage}
          </Link>
        </>
      ) : (
        <p className="text-xs text-muted-foreground">{T.staffNote}</p>
      )}

      {admin && (pending?.kind === "attach" || pending?.kind === "filter") ? (
        <AttachDialog
          key={`${pending.channel.id}-${pending.kind}`}
          open
          onOpenChange={(o) => {
            if (!o) setPending(null);
          }}
          catalog={catalog}
          fixedJob={job}
          initial={pending.kind === "filter" ? pending.initial : undefined}
          attachedJobIds={[]}
          onSubmit={async (jobId, f) => {
            await attach({
              variables: {
                jobId,
                channelId: pending.channel.id,
                filter: toFilterInput(f, catalog),
              },
            });
            await reload();
            toast.success(pending.kind === "filter" ? T.filterSaved : T.attachedToast);
          }}
        />
      ) : null}
      {admin ? (
        <ConfirmDialog
          open={pending?.kind === "detach"}
          onOpenChange={(o) => {
            if (!o) setPending(null);
          }}
          title={pending?.kind === "detach" ? T.detachTitle(pending.channel.name) : ""}
          description={T.detachLead}
          confirmLabel={T.detachConfirm}
          destructive
          onConfirm={async () => {
            if (pending?.kind !== "detach") return;
            try {
              await detach({ variables: { jobId: job.id, channelId: pending.channel.id } });
              await reload();
              toast.success(T.detachedToast);
            } catch (err) {
              reportActionError(err);
              throw err;
            }
          }}
        />
      ) : null}
    </div>
  );
}

function AttachmentRow({
  attachment: a,
  catalog,
  admin,
  onEdit,
  onDetach,
}: {
  attachment: Attachment;
  catalog: readonly CatalogGroup[];
  admin: boolean;
  onEdit: () => void;
  onDetach: () => void;
}) {
  const webhook = a.type === "WEBHOOK";
  const Icon = webhook ? Webhook : Mail;
  return (
    <li className="flex items-center gap-3 px-3 py-2" data-testid="job-shared-channel">
      <Icon aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
      <div className="flex min-w-0 flex-1 flex-col">
        <span className="flex items-center gap-2 truncate text-sm font-medium">
          {a.name}
          {!a.enabled ? (
            <Tag compact tone="warning">
              {T.disabled}
            </Tag>
          ) : null}
        </span>
        <span className="truncate font-mono text-2xs text-muted-foreground">
          {webhook ? (a.displayUrl ?? "") : T.emailList}
        </span>
        <span className="truncate text-xs text-muted-foreground">
          {describeFilter({ groups: a.groups, events: a.events }, catalog)}
        </span>
      </div>
      {admin ? (
        <>
          <Button variant="ghost" size="icon-sm" aria-label={T.editFilter(a.name)} onClick={onEdit}>
            <Pencil aria-hidden="true" />
          </Button>
          <Button variant="ghost" size="icon-sm" aria-label={T.detach(a.name)} onClick={onDetach}>
            <Unlink aria-hidden="true" />
          </Button>
        </>
      ) : null}
    </li>
  );
}

/** The channel picker of admins (sharedChannels is admin only). */
function AttachForm({
  attachedIds,
  onPick,
}: {
  attachedIds: readonly string[];
  onPick: (channel: Target) => void;
}) {
  const selectId = useId();
  const { data, loading, error } = useQuery(SharedChannelsQuery);
  const [chosen, setChosen] = useState("");

  if (loading && !data) return <Skeleton className="h-10" />;
  if (error && !data) {
    return <p className="text-sm text-destructive">{userMessage(error)}</p>;
  }
  const channels = getFragmentData(SharedChannelFields, data?.sharedChannels ?? []);
  const attached = new Set(attachedIds);
  const available = channels.filter((c) => !attached.has(c.id));
  const selected = available.find((c) => c.id === chosen) ?? null;

  if (channels.length === 0) {
    return <p className="text-sm text-muted-foreground">{T.noChannels}</p>;
  }
  if (available.length === 0) {
    return <p className="text-xs text-muted-foreground">{T.allAttached}</p>;
  }
  return (
    <form
      className="flex flex-wrap items-end gap-2"
      onSubmit={(e) => {
        e.preventDefault();
        if (selected) {
          onPick({ id: selected.id, name: selected.name });
          setChosen("");
        }
      }}
    >
      <div className="flex min-w-48 flex-1 flex-col gap-1.5">
        <label htmlFor={selectId} className="eyebrow">
          {T.pick}
        </label>
        <NativeSelect
          id={selectId}
          value={selected ? selected.id : ""}
          onChange={(e) => {
            setChosen(e.target.value);
          }}
        >
          <option value="">{T.pickPlaceholder}</option>
          {available.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name}
            </option>
          ))}
        </NativeSelect>
      </div>
      <Button type="submit" variant="outline" disabled={!selected}>
        <Plus aria-hidden="true" /> {T.attach}
      </Button>
    </form>
  );
}
