import { useMutation } from "@apollo/client/react";
import { UserMinus, UserPlus } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { useSessionUser } from "@/features/auth/use-session-user";
import { reportActionError, useStepUp } from "@/features/auth/use-step-up";
import { UserPicker } from "@/features/users/user-picker";
import { type PickedUser } from "@/features/users/user-picker-model";
import type { JobDetailQuery as JobDetailResult } from "@/graphql/gen/graphql";
import { formatDateTime } from "@/lib/format";
import { defineMessages } from "@/lib/i18n";
import { messages } from "@/lib/messages";
import { AssignJobOwnersMutation, RemoveJobOwnerMutation } from "@/lib/operations";

import { JobDetailQuery } from "./operations";

type Job = NonNullable<JobDetailResult["job"]>;

/** Role names in the page's language (the shared shell table). */
const ROLE_NAMES: Readonly<Record<string, string>> = messages.roles;

const T = defineMessages({
  en: {
    none: "No backup owners. Owners (role User) can view this job, run it and download its backups.",
    roleNote: (role: string) =>
      `${role.toLowerCase()} has no owner rights: only accounts with role User gain rights from ownership`,
    disabled: "disabled",
    added: (at: string) => `added ${at}`,
    remove: (name: string) => `Remove ${name}`,
    removeTitle: (name: string) => `Remove ${name} as owner?`,
    removeLead:
      "They lose access to this job, its runs and downloads at once. Audited and alerted.",
    removed: "Owner removed.",
    add: (n: number) => (n > 1 ? `Add ${String(n)} owners` : "Add owner"),
    addedToast: (n: number) => (n > 1 ? `${String(n)} owners added.` : "Owner added."),
    picker: "Add backup owners",
    selfNote: "Maintainers cannot add themselves.",
    removeConfirm: "Remove owner",
  },
  nl: {
    none: "Geen back-upeigenaren. Eigenaren (rol Gebruiker) kunnen deze taak bekijken, uitvoeren en de back-ups ervan downloaden.",
    roleNote: (role: string) =>
      `Rol ${(Object.hasOwn(ROLE_NAMES, role) ? ROLE_NAMES[role] : undefined) ?? role} heeft geen eigenaarsrechten: alleen accounts met de rol Gebruiker krijgen rechten door eigenaarschap`,
    disabled: "uitgeschakeld",
    added: (at: string) => `toegevoegd ${at}`,
    remove: (name: string) => `${name} verwijderen`,
    removeTitle: (name: string) => `${name} als eigenaar verwijderen?`,
    removeLead:
      "Diegene verliest direct de toegang tot deze taak, de runs en de downloads. Wordt geaudit en gemeld.",
    removed: "Eigenaar verwijderd.",
    add: (n: number) => (n > 1 ? `${String(n)} eigenaren toevoegen` : "Eigenaar toevoegen"),
    addedToast: (n: number) =>
      n > 1 ? `${String(n)} eigenaren toegevoegd.` : "Eigenaar toegevoegd.",
    picker: "Back-upeigenaren toevoegen",
    selfNote: "Onderhouders kunnen zichzelf niet toevoegen.",
    removeConfirm: "Eigenaar verwijderen",
  },
});

/**
 * Backup owners of a job (staff). Adding and removing need a fresh step-up
 * (assignJobOwners / removeJobOwner are @stepUp). Candidates come from
 * the shared UserPicker over the staff user directory (D-184); only active
 * accounts with role User can be chosen (only they gain owner rights,
 * D-067). The server re-checks every grant.
 */
export function OwnersPanel({ job, admin }: { job: Job; admin: boolean }) {
  const { withStepUp } = useStepUp();
  const refetch = { refetchQueries: [JobDetailQuery], awaitRefetchQueries: true };
  const [assign, assignState] = useMutation(AssignJobOwnersMutation, refetch);
  const [remove] = useMutation(RemoveJobOwnerMutation, refetch);
  const [removing, setRemoving] = useState<{ userId: string; name: string } | null>(null);
  const [picked, setPicked] = useState<PickedUser[]>([]);
  const me = useSessionUser();
  const owners = job.owners ?? [];
  // Existing owners are not offered again; maintainers cannot add themselves.
  const exclude = [...owners.map((o) => o.userId), ...(admin ? [] : [me.id])];

  const add = async () => {
    const ids = picked.map((u) => u.id);
    if (ids.length === 0) return;
    try {
      await withStepUp(() => assign({ variables: { jobId: job.id, users: ids } }));
      setPicked([]);
      toast.success(T.addedToast(ids.length));
    } catch (err) {
      reportActionError(err);
    }
  };

  return (
    <div className="flex flex-col gap-4">
      {owners.length === 0 ? (
        <p className="text-sm text-muted-foreground">{T.none}</p>
      ) : (
        <ul className="flex flex-col divide-y divide-rule rounded-md border">
          {owners.map((o) => (
            <li key={o.userId} className="flex items-center gap-3 px-3 py-2">
              <div className="flex min-w-0 flex-1 flex-col">
                <span className="truncate text-sm font-medium">
                  {o.displayName || o.email}
                  {o.disabled ? (
                    <span className="ml-2 font-mono text-2xs text-muted-foreground uppercase">
                      {T.disabled}
                    </span>
                  ) : null}
                </span>
                <span className="truncate font-mono text-2xs text-muted-foreground">
                  {o.email} · {T.added(formatDateTime(o.addedAt))}
                </span>
                {o.role !== "USER" ? (
                  <span className="text-2xs text-status-partial-fg">{T.roleNote(o.role)}</span>
                ) : null}
              </div>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={T.remove(o.displayName || o.email)}
                onClick={() => {
                  setRemoving({ userId: o.userId, name: o.displayName || o.email });
                }}
              >
                <UserMinus aria-hidden="true" />
              </Button>
            </li>
          ))}
        </ul>
      )}

      <form
        className="flex flex-col gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          void add();
        }}
      >
        <UserPicker label={T.picker} value={picked} onChange={setPicked} excludeIds={exclude} />

        {!admin ? <p className="text-xs text-muted-foreground">{T.selfNote}</p> : null}
        <Button
          type="submit"
          variant="outline"
          disabled={picked.length === 0 || assignState.loading}
          className="self-start"
        >
          <UserPlus aria-hidden="true" /> {T.add(picked.length)}
        </Button>
      </form>

      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(o) => {
          if (!o) setRemoving(null);
        }}
        title={T.removeTitle(removing?.name ?? "")}
        description={T.removeLead}
        confirmLabel={T.removeConfirm}
        destructive
        onConfirm={async () => {
          if (!removing) return;
          try {
            await withStepUp(() =>
              remove({ variables: { jobId: job.id, userId: removing.userId } }),
            );
            toast.success(T.removed);
          } catch (err) {
            reportActionError(err);
            throw err;
          }
        }}
      />
    </div>
  );
}
