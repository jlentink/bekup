import { skipToken, useQuery } from "@apollo/client/react";
import { Loader2, Search } from "lucide-react";
import { useId, useState, type SyntheticEvent } from "react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { isStepUpCancelled } from "@/features/auth/use-step-up";
import type { NotificationGroup, SharedChannelType } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { messages } from "@/lib/messages";
import { cn } from "@/lib/utils";

import { JobsQuery } from "../operations";
import { textareaClass } from "../ui";
import { attachableCatalog, parseAddresses, type CatalogGroup, type FilterDraft } from "./filter";

/*
 * The form dialogs keep their draft in state initialised from props: the
 * caller mounts them only while open (with a key per subject), so every
 * opening starts from the current values.
 */

/* ---------------- Create / edit ---------------- */

export interface ChannelDraft {
  name: string;
  type: SharedChannelType;
  addresses: string[];
  /** Webhook URL; empty on edit keeps the stored one. */
  url: string;
}

const F = {
  createTitle: "New shared channel",
  editTitle: "Edit shared channel",
  createLead:
    "An e-mail list or a webhook that receives the events of the jobs you attach it to. Security alerts are never sent to shared channels.",
  editLead: "Changes are audited. A new webhook URL keeps the signing key; rotate it separately.",
  type: "Type",
  types: { EMAIL_LIST: "E-mail list", WEBHOOK: "Webhook" } satisfies Record<
    SharedChannelType,
    string
  >,
  typeHints: {
    EMAIL_LIST: "Up to 50 addresses, e.g. an on-call list.",
    WEBHOOK: "HTTPS endpoint (Slack, Teams, your own receiver); requests are HMAC signed.",
  } satisfies Record<SharedChannelType, string>,
  name: "Name",
  addresses: "Addresses",
  addressesHint: "One per line, or separated by commas.",
  url: "Webhook URL",
  urlEdit: "New webhook URL (optional)",
  urlHint:
    "https only; it must pass the SSRF guard. The URL may contain a credential, so it is write-only: afterwards only scheme, host and port are shown.",
  urlEditHint: "Leave empty to keep the stored URL. Stored URL: ",
  create: "Create channel",
  save: "Save changes",
  nameMissing: "Enter a name (1 to 100 characters).",
  addressesMissing: "Enter 1 to 50 e-mail addresses.",
  urlMissing: "Enter an https:// URL.",
} as const;

export function ChannelFormDialog({
  open,
  onOpenChange,
  initial,
  displayUrl,
  onSubmit,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Present: edit mode. */
  initial?: { name: string; type: SharedChannelType; addresses: readonly string[] } | undefined;
  displayUrl?: string | null | undefined;
  onSubmit: (d: ChannelDraft) => Promise<void>;
}) {
  const editing = initial !== undefined;
  const [type, setType] = useState<SharedChannelType>(initial?.type ?? "WEBHOOK");
  const [name, setName] = useState(initial?.name ?? "");
  const [addresses, setAddresses] = useState(initial?.addresses.join("\n") ?? "");
  const [url, setUrl] = useState("");
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);
  const ids = { name: useId(), addresses: useId(), url: useId() };

  const submit = (e: SyntheticEvent) => {
    e.preventDefault();
    const list = parseAddresses(addresses);
    const urlOk = /^https:\/\/\S+$/i.test(url.trim());
    const problem =
      name.trim() === "" || name.trim().length > 100
        ? F.nameMissing
        : type === "EMAIL_LIST" && (list.length === 0 || list.length > 50)
          ? F.addressesMissing
          : type === "WEBHOOK" && (editing ? url.trim() !== "" && !urlOk : !urlOk)
            ? F.urlMissing
            : undefined;
    if (problem) {
      setError(problem);
      return;
    }
    setError(undefined);
    setBusy(true);
    onSubmit({ name: name.trim(), type, addresses: list, url: url.trim() })
      .then(() => {
        setUrl("");
        onOpenChange(false);
      })
      .catch((err: unknown) => {
        if (!isStepUpCancelled(err)) setError(userMessage(err));
      })
      .finally(() => {
        setBusy(false);
      });
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!busy) onOpenChange(o);
      }}
    >
      <DialogContent className="gap-5 sm:max-w-xl">
        <DialogHeader>
          <DialogTitle className="font-display text-2xl font-normal">
            {editing ? F.editTitle : F.createTitle}
          </DialogTitle>
          <DialogDescription>{editing ? F.editLead : F.createLead}</DialogDescription>
        </DialogHeader>
        <form noValidate onSubmit={submit} className="flex flex-col gap-4">
          {!editing ? (
            <fieldset className="grid gap-2 sm:grid-cols-2">
              <legend className="mb-2 text-sm font-medium">{F.type}</legend>
              {(["WEBHOOK", "EMAIL_LIST"] as const).map((t) => (
                <label
                  key={t}
                  className={cn(
                    "flex cursor-pointer flex-col gap-1 rounded-md border bg-surface p-3 has-[:focus-visible]:ring-[3px] has-[:focus-visible]:ring-ring/50",
                    type === t ? "border-primary/50 bg-accent" : "hover:border-border-strong",
                  )}
                >
                  <span className="flex items-center gap-2 font-medium">
                    <input
                      type="radio"
                      name="channel-type"
                      className="size-4 accent-primary"
                      checked={type === t}
                      onChange={() => {
                        setType(t);
                      }}
                    />
                    {F.types[t]}
                  </span>
                  <span className="text-xs text-muted-foreground">{F.typeHints[t]}</span>
                </label>
              ))}
            </fieldset>
          ) : null}
          <div className="grid gap-2">
            <Label htmlFor={ids.name}>{F.name}</Label>
            <Input
              id={ids.name}
              value={name}
              maxLength={100}
              autoComplete="off"
              onChange={(e) => {
                setName(e.target.value);
              }}
            />
          </div>
          {type === "EMAIL_LIST" ? (
            <div className="grid gap-2">
              <Label htmlFor={ids.addresses}>{F.addresses}</Label>
              <textarea
                id={ids.addresses}
                rows={4}
                spellCheck={false}
                className={textareaClass}
                value={addresses}
                onChange={(e) => {
                  setAddresses(e.target.value);
                }}
              />
              <p className="text-xs text-muted-foreground">{F.addressesHint}</p>
            </div>
          ) : (
            <div className="grid gap-2">
              <Label htmlFor={ids.url}>{editing ? F.urlEdit : F.url}</Label>
              <Input
                id={ids.url}
                type="url"
                inputMode="url"
                autoComplete="off"
                spellCheck={false}
                placeholder="https://hooks.example.com/…"
                value={url}
                onChange={(e) => {
                  setUrl(e.target.value);
                }}
              />
              <p className="text-xs text-muted-foreground">
                {editing && displayUrl ? (
                  <>
                    {F.urlEditHint}
                    <span className="font-mono">{displayUrl}</span>
                  </>
                ) : (
                  F.urlHint
                )}
              </p>
            </div>
          )}
          {error ? (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          ) : null}
          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" variant="outline" disabled={busy}>
                {messages.common.cancel}
              </Button>
            </DialogClose>
            <Button type="submit" disabled={busy}>
              {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
              {editing ? F.save : F.create}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/* ---------------- Signing key, shown once ---------------- */

/* ---------------- Attach to a job with an event filter ---------------- */

const A = {
  title: "Attach to a job",
  editTitle: (job: string) => `Events from ${job}`,
  lead: "Pick the job and the events the channel receives from it. Nothing picked: every job event. Security alerts are never sent to shared channels.",
  job: "Job",
  search: "Search jobs",
  noJobs: "No jobs match.",
  attached: "attached",
  events: "Events",
  wholeGroup: (label: string) => `All ${label.toLowerCase()} events`,
  allEvents: "Nothing picked: the channel receives every job event.",
  securityNote: "Security events are excluded: they only reach admins personally.",
  attach: "Attach",
  save: "Save filter",
  pickJob: "Pick a job.",
} as const;

export function AttachDialog({
  open,
  onOpenChange,
  catalog,
  fixedJob,
  initial,
  attachedJobIds,
  onSubmit,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  catalog: readonly CatalogGroup[];
  /** Edit the filter of an existing attachment. */
  fixedJob?: { id: string; name: string } | undefined;
  initial?: FilterDraft | undefined;
  attachedJobIds: readonly string[];
  onSubmit: (jobId: string, filter: FilterDraft) => Promise<void>;
}) {
  const allowed = attachableCatalog(catalog);
  const [jobId, setJobId] = useState<string | null>(fixedJob?.id ?? null);
  const [groups, setGroups] = useState<NotificationGroup[]>([...(initial?.groups ?? [])]);
  const [events, setEvents] = useState<string[]>([...(initial?.events ?? [])]);
  const [search, setSearch] = useState("");
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);
  const searchId = useId();

  const { data: jobs, loading } = useQuery(
    JobsQuery,
    open && fixedJob === undefined
      ? { variables: { first: 20, filter: search.trim() ? { search: search.trim() } : null } }
      : skipToken,
  );

  const toggleGroup = (g: NotificationGroup, on: boolean) => {
    setGroups((cur) => (on ? [...cur, g] : cur.filter((x) => x !== g)));
  };
  const toggleEvent = (t: string, on: boolean) => {
    setEvents((cur) => (on ? [...cur, t] : cur.filter((x) => x !== t)));
  };

  const submit = (e: SyntheticEvent) => {
    e.preventDefault();
    if (!jobId) {
      setError(A.pickJob);
      return;
    }
    setError(undefined);
    setBusy(true);
    onSubmit(jobId, { groups, events })
      .then(() => {
        onOpenChange(false);
      })
      .catch((err: unknown) => {
        if (!isStepUpCancelled(err)) setError(userMessage(err));
      })
      .finally(() => {
        setBusy(false);
      });
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!busy) onOpenChange(o);
      }}
    >
      <DialogContent className="max-h-[90vh] gap-5 overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="font-display text-2xl font-normal">
            {fixedJob ? A.editTitle(fixedJob.name) : A.title}
          </DialogTitle>
          <DialogDescription>{A.lead}</DialogDescription>
        </DialogHeader>
        <form noValidate onSubmit={submit} className="flex flex-col gap-5">
          {!fixedJob ? (
            <fieldset className="flex flex-col gap-2">
              <legend className="mb-2 text-sm font-medium">{A.job}</legend>
              <div className="relative">
                <Search
                  aria-hidden="true"
                  className="pointer-events-none absolute top-1/2 left-3 size-3.5 -translate-y-1/2 text-muted-foreground"
                />
                <Label htmlFor={searchId} className="sr-only">
                  {A.search}
                </Label>
                <Input
                  id={searchId}
                  className="pl-8"
                  placeholder={A.search}
                  value={search}
                  onChange={(e) => {
                    setSearch(e.target.value);
                  }}
                />
              </div>
              <div className="max-h-44 overflow-y-auto rounded-md border">
                {loading && !jobs ? (
                  <p className="p-3 text-sm text-muted-foreground">{messages.common.loading}…</p>
                ) : (jobs?.jobs.edges.length ?? 0) === 0 ? (
                  <p className="p-3 text-sm text-muted-foreground">{A.noJobs}</p>
                ) : (
                  jobs?.jobs.edges.map(({ node }) => {
                    const already = attachedJobIds.includes(node.id);
                    return (
                      <label
                        key={node.id}
                        className={cn(
                          "flex cursor-pointer items-center gap-3 border-b px-3 py-2 text-sm last:border-b-0 hover:bg-accent/60",
                          jobId === node.id && "bg-accent",
                        )}
                      >
                        <input
                          type="radio"
                          name="attach-job"
                          className="size-4 accent-primary"
                          checked={jobId === node.id}
                          onChange={() => {
                            setJobId(node.id);
                          }}
                        />
                        <span className="font-medium">{node.name}</span>
                        <span className="font-mono text-2xs text-muted-foreground">
                          {node.slug}
                        </span>
                        {already ? <span className="eyebrow ml-auto">{A.attached}</span> : null}
                      </label>
                    );
                  })
                )}
              </div>
            </fieldset>
          ) : null}

          <fieldset className="flex flex-col gap-3" data-testid="event-filter">
            <legend className="mb-1 text-sm font-medium">{A.events}</legend>
            {allowed.map((g) => {
              const whole = groups.includes(g.group);
              return (
                <div key={g.group} className="rounded-md border bg-surface">
                  <label className="flex cursor-pointer items-center gap-2.5 border-b px-3 py-2 text-sm font-medium">
                    <input
                      type="checkbox"
                      className="size-4 accent-primary"
                      checked={whole}
                      onChange={(e) => {
                        toggleGroup(g.group, e.target.checked);
                      }}
                    />
                    {A.wholeGroup(g.label)}
                  </label>
                  <div className="grid gap-x-4 gap-y-1.5 px-3 py-2.5 sm:grid-cols-2">
                    {g.events.map((ev) => (
                      <label
                        key={ev.type}
                        className={cn(
                          "flex cursor-pointer items-center gap-2 text-sm",
                          whole && "cursor-not-allowed text-muted-foreground",
                        )}
                      >
                        <input
                          type="checkbox"
                          className="size-3.5 accent-primary"
                          disabled={whole}
                          checked={whole || events.includes(ev.type)}
                          onChange={(e) => {
                            toggleEvent(ev.type, e.target.checked);
                          }}
                        />
                        <span>{ev.label}</span>
                        <span className="font-mono text-2xs text-muted-foreground">{ev.type}</span>
                      </label>
                    ))}
                  </div>
                </div>
              );
            })}
            <p className="text-xs text-muted-foreground">
              {groups.length === 0 && events.length === 0 ? `${A.allEvents} ` : ""}
              {A.securityNote}
            </p>
          </fieldset>

          {error ? (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          ) : null}
          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" variant="outline" disabled={busy}>
                {messages.common.cancel}
              </Button>
            </DialogClose>
            <Button type="submit" disabled={busy}>
              {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
              {fixedJob ? A.save : A.attach}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
