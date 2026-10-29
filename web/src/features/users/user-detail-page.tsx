import { useMutation, useQuery } from "@apollo/client/react";
import {
  ArrowLeft,
  Fingerprint,
  KeyRound,
  Loader2,
  Lock,
  Trash2,
  UserRound,
  type LucideIcon,
} from "lucide-react";
import { useId, useState, type ReactNode } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
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
import { Skeleton } from "@/components/ui/skeleton";
import { Section } from "@/features/account/section";
import { useSessionUser } from "@/features/auth/use-session-user";
import { isStepUpCancelled, reportActionError, useStepUp } from "@/features/auth/use-step-up";
import { getFragmentData } from "@/graphql/gen";
import type { AdminUserFieldsFragment, Role } from "@/graphql/gen/graphql";
import { ApiCode, applyFieldErrors, hasApiCode, userMessage } from "@/lib/api-error";
import { formatDateTime, formatRelative } from "@/lib/format";
import { messages } from "@/lib/messages";

import { reportUserAdminError } from "./errors";
import { usersMessages as M } from "./messages";
import {
  AdminUserFields,
  DeleteUserMutation,
  DisableUserMutation,
  EnableUserMutation,
  ResetUserPasswordMutation,
  ResetUserSecondFactorsMutation,
  UnlockUserMutation,
  UpdateUserMutation,
  UserQuery,
  UserSessionsQuery,
  UsersQuery,
} from "./operations";
import { OwnedJobsSection } from "./owned-jobs";
import { RoleField } from "./role-field";
import { accountFlags } from "./status";
import { TemporaryPasswordDialog, type IssuedPassword } from "./temporary-password-dialog";
import { AccountFlags, LastSeen, Monogram, RoleTag, TwoFactorCell } from "./user-badges";
import { UserSessionsSection } from "./user-sessions";

type AdminUser = AdminUserFieldsFragment;

/** One account (admin): profile, access, second factors, sessions, jobs, delete. */
export function UserDetailPage() {
  const { userId = "" } = useParams();
  const { data, error, refetch } = useQuery(UserQuery, {
    variables: { id: userId },
    fetchPolicy: "cache-and-network",
  });
  const user = data?.user ? getFragmentData(AdminUserFields, data.user) : null;

  return (
    <div className="flex flex-col gap-6">
      <Link
        to="/admin/users"
        className="inline-flex items-center gap-1.5 self-start text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft aria-hidden="true" className="size-3.5" /> {M.back}
      </Link>
      {error && !data ? (
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
      ) : !data ? (
        <div className="flex flex-col gap-3" aria-busy="true">
          <Skeleton className="h-16 w-full max-w-md" />
          <Skeleton className="h-40 w-full" />
        </div>
      ) : !user ? (
        <p className="py-10 text-muted-foreground">{M.notFound}</p>
      ) : (
        <UserDetail user={user} />
      )}
    </div>
  );
}

function UserDetail({ user }: { user: AdminUser }) {
  const me = useSessionUser();
  const self = user.id === me.id;
  const deleted = user.deletedAt !== null;
  return (
    <>
      <header className="flex flex-col gap-4 border-b border-rule pb-6 sm:flex-row sm:items-end sm:justify-between">
        <div className="flex items-center gap-4">
          <Monogram name={user.displayName} className="size-14 text-2xl" />
          <div className="flex min-w-0 flex-col gap-1">
            <p className="eyebrow">{M.eyebrow}</p>
            <h1 className="font-display text-display leading-none tracking-tight break-words">
              {user.displayName}
            </h1>
            <p className="font-mono text-[0.8125rem] break-all text-muted-foreground">
              {user.email}
            </p>
          </div>
        </div>
        <div className="flex flex-col items-start gap-2 sm:items-end">
          <div className="flex items-center gap-2">
            <RoleTag role={user.role} />
            {self ? (
              <span className="eyebrow rounded-sm border border-rule px-1.5">{M.youTag}</span>
            ) : null}
          </div>
          <AccountFlags user={user} />
          <TwoFactorCell user={user} />
          <span className="font-mono text-2xs text-muted-foreground">
            {M.columns.lastSeen}: <LastSeen at={user.lastSeenAt} />
          </span>
        </div>
      </header>
      {deleted ? (
        <Alert>
          <Trash2 aria-hidden="true" />
          <AlertDescription>{M.deletedBanner(formatDateTime(user.deletedAt))}</AlertDescription>
        </Alert>
      ) : null}
      <div className="max-w-5xl">
        {!deleted ? <ProfileSection user={user} self={self} /> : null}
        {!deleted ? <AccessSection user={user} self={self} /> : null}
        {!deleted ? <SecondFactorSection user={user} self={self} /> : null}
        <UserSessionsSection userId={user.id} readOnly={deleted} />
        <OwnedJobsSection subject={user} />
        {!deleted ? <DeleteSection user={user} self={self} /> : null}
      </div>
    </>
  );
}

function ProfileSection({ user, self }: { user: AdminUser; self: boolean }) {
  const { withStepUp } = useStepUp();
  const [update] = useMutation(UpdateUserMutation, {
    refetchQueries: [UserSessionsQuery],
  });
  const nameId = useId();
  const selfNoteId = useId();
  const [name, setName] = useState(user.displayName);
  const [role, setRole] = useState<Role>(user.role);
  const [nameError, setNameError] = useState<string>();
  const [confirmRole, setConfirmRole] = useState(false);
  const [busy, setBusy] = useState(false);

  const trimmed = name.trim();
  const nameChanged = trimmed !== user.displayName;
  const roleChanged = role !== user.role;

  const save = async () => {
    setBusy(true);
    try {
      await withStepUp(() =>
        update({
          variables: {
            id: user.id,
            input: {
              ...(nameChanged ? { displayName: trimmed } : {}),
              ...(roleChanged ? { role } : {}),
            },
          },
        }),
      );
      toast.success(M.saved);
    } catch (err) {
      if (
        !applyFieldErrors<{ displayName: string }>(
          err,
          (_f, e) => {
            setNameError(e.message);
          },
          ["displayName"],
        )
      ) {
        reportUserAdminError(err);
      }
      throw err;
    } finally {
      setBusy(false);
    }
  };

  const onSubmit = () => {
    setNameError(undefined);
    if (!nameChanged && !roleChanged) {
      toast.info(M.noChanges);
      return;
    }
    if (trimmed === "") {
      setNameError(M.nameRequired);
      return;
    }
    if (trimmed.length > 200) {
      setNameError(M.nameTooLong);
      return;
    }
    if (roleChanged) {
      setConfirmRole(true);
      return;
    }
    void save().catch(() => undefined);
  };

  return (
    <Section icon={UserRound} title={M.profileHeading} lead={M.profileLead}>
      <form
        noValidate
        className="flex flex-col gap-5"
        onSubmit={(e) => {
          e.preventDefault();
          onSubmit();
        }}
      >
        <div className="flex max-w-sm flex-col gap-2">
          <Label htmlFor={nameId}>{M.displayName}</Label>
          <Input
            id={nameId}
            value={name}
            aria-invalid={nameError ? true : undefined}
            aria-describedby={nameError ? `${nameId}-error` : undefined}
            onChange={(e) => {
              setName(e.target.value);
            }}
          />
          {nameError ? (
            <p id={`${nameId}-error`} className="text-sm text-destructive">
              {nameError}
            </p>
          ) : null}
        </div>
        <RoleField
          name={`role-${user.id}`}
          value={role}
          onChange={setRole}
          disabled={self}
          describedBy={self ? selfNoteId : undefined}
        />
        {self ? (
          <p id={selfNoteId} className="text-xs text-muted-foreground">
            {M.selfRoleNote}
          </p>
        ) : roleChanged ? (
          <p className="text-xs text-status-partial-fg">
            {M.roleChange(messages.roles[user.role], messages.roles[role])}
            {role !== "USER" ? ` ${M.roleOwnedJobsNote}` : ""}
          </p>
        ) : null}
        <Button type="submit" size="sm" className="self-start" disabled={busy}>
          {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
          {M.save}
        </Button>
      </form>
      <ConfirmDialog
        open={confirmRole}
        onOpenChange={setConfirmRole}
        title={M.roleChangeTitle}
        description={M.roleChange(messages.roles[user.role], messages.roles[role])}
        confirmLabel={M.roleChangeConfirm}
        destructive
        onConfirm={save}
      />
    </Section>
  );
}

function ActionRow({
  icon: Icon,
  title,
  body,
  action,
}: {
  icon: LucideIcon;
  title: string;
  body: ReactNode;
  action: ReactNode;
}) {
  return (
    <div className="flex flex-col gap-3 py-3 first:pt-0 last:pb-0 sm:flex-row sm:items-center sm:justify-between">
      <div className="flex gap-3">
        <Icon aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
        <div className="flex flex-col gap-0.5">
          <p className="text-sm font-medium">{title}</p>
          <div className="text-xs text-muted-foreground">{body}</div>
        </div>
      </div>
      <div className="shrink-0">{action}</div>
    </div>
  );
}

function AccessSection({ user, self }: { user: AdminUser; self: boolean }) {
  const { withStepUp } = useStepUp();
  const [disable] = useMutation(DisableUserMutation, { refetchQueries: [UserSessionsQuery] });
  const [enable] = useMutation(EnableUserMutation);
  const [unlock] = useMutation(UnlockUserMutation);
  const [reset] = useMutation(ResetUserPasswordMutation, {
    fetchPolicy: "no-cache",
    refetchQueries: [UserQuery, UserSessionsQuery],
  });
  const [confirm, setConfirm] = useState<"disable" | "reset" | null>(null);
  const [issued, setIssued] = useState<IssuedPassword | null>(null);
  const [busy, setBusy] = useState(false);
  const flags = accountFlags(user);
  const locked = flags.includes("locked");

  const run = async (op: () => Promise<unknown>, success: string) => {
    setBusy(true);
    try {
      await withStepUp(op);
      toast.success(success);
    } catch (err) {
      reportUserAdminError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Section icon={Lock} title={M.accessHeading} lead={M.accessLead}>
      <div className="divide-y divide-rule rounded-md border bg-surface px-4 py-3">
        <ActionRow
          icon={Lock}
          title={user.disabled ? M.status.disabled : M.status.active}
          body={user.disabled ? M.disabled : M.disableLead}
          action={
            self ? null : user.disabled ? (
              <Button
                variant="outline"
                size="sm"
                disabled={busy}
                onClick={() => {
                  void run(() => enable({ variables: { id: user.id } }), M.enabled);
                }}
              >
                {M.enable}
              </Button>
            ) : (
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  setConfirm("disable");
                }}
              >
                {M.disable}
              </Button>
            )
          }
        />
        {locked ? (
          <ActionRow
            icon={Lock}
            title={M.status.locked}
            body={M.lockedUntil(formatDateTime(user.lockedUntil))}
            action={
              <Button
                variant="outline"
                size="sm"
                disabled={busy}
                onClick={() => {
                  void run(() => unlock({ variables: { id: user.id } }), M.unlocked);
                }}
              >
                {M.unlock}
              </Button>
            }
          />
        ) : null}
        <ActionRow
          icon={KeyRound}
          title={M.resetPassword}
          body={
            self
              ? M.selfResetPassword
              : flags.includes("temporaryExpired")
                ? M.temporaryExpired
                : flags.includes("temporary") && user.temporaryPasswordExpiresAt
                  ? M.temporaryPending(formatRelative(user.temporaryPasswordExpiresAt))
                  : M.resetPasswordLead
          }
          action={
            self ? null : (
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  setConfirm("reset");
                }}
              >
                {M.resetPassword}
              </Button>
            )
          }
        />
      </div>
      <ConfirmDialog
        open={confirm === "disable"}
        onOpenChange={(o) => {
          if (!o) setConfirm(null);
        }}
        title={M.disableTitle}
        description={M.disableLead}
        confirmLabel={M.disable}
        destructive
        onConfirm={async () => {
          try {
            await withStepUp(() => disable({ variables: { id: user.id } }));
          } catch (err) {
            reportUserAdminError(err);
            throw err;
          }
          toast.success(M.disabled);
        }}
      />
      <ConfirmDialog
        open={confirm === "reset"}
        onOpenChange={(o) => {
          if (!o) setConfirm(null);
        }}
        title={M.resetPasswordTitle}
        description={M.resetPasswordLead}
        confirmLabel={M.resetPassword}
        destructive
        onConfirm={async () => {
          try {
            const res = await withStepUp(() => reset({ variables: { id: user.id } }));
            const tp = res.data?.resetUserPassword;
            if (!tp) throw new Error("empty resetUserPassword result");
            setIssued({
              email: user.email,
              password: tp.password,
              expiresAt: tp.expiresAt,
              kind: "reset",
            });
          } catch (err) {
            reportActionError(err);
            throw err;
          }
        }}
      />
      <TemporaryPasswordDialog
        issued={issued}
        onDone={() => {
          setIssued(null);
        }}
      />
    </Section>
  );
}

function SecondFactorSection({ user, self }: { user: AdminUser; self: boolean }) {
  const { withStepUp } = useStepUp();
  const [resetFactors] = useMutation(ResetUserSecondFactorsMutation, {
    refetchQueries: [UserSessionsQuery],
  });
  const [confirming, setConfirming] = useState(false);
  const noteId = useId();
  return (
    <Section icon={Fingerprint} title={M.twoFactorHeading} lead={M.twoFactorLead}>
      <div className="flex flex-col gap-3">
        <TwoFactorCell user={user} />
        {self ? (
          <p id={noteId} className="text-sm text-muted-foreground">
            {M.selfResetFactors}{" "}
            <Link
              to="/profile/security"
              className="text-primary underline-offset-4 hover:underline"
            >
              {messages.shell.profile}
            </Link>
          </p>
        ) : null}
        <Button
          variant="outline"
          size="sm"
          className="self-start"
          disabled={self}
          aria-describedby={self ? noteId : undefined}
          onClick={() => {
            setConfirming(true);
          }}
        >
          {M.resetFactors}
        </Button>
      </div>
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={M.resetFactorsTitle}
        description={M.resetFactorsLead}
        confirmLabel={M.resetFactors}
        destructive
        onConfirm={async () => {
          try {
            const res = await withStepUp(() => resetFactors({ variables: { userId: user.id } }));
            const r = res.data?.resetUserSecondFactors;
            toast.success(
              r
                ? M.resetFactorsDone(r.removedTotp, r.removedPasskeys, r.removedRecoveryCodes)
                : M.saved,
            );
          } catch (err) {
            reportActionError(err);
            throw err;
          }
        }}
      />
    </Section>
  );
}

function DeleteSection({ user, self }: { user: AdminUser; self: boolean }) {
  const [open, setOpen] = useState(false);
  return (
    <Section icon={Trash2} title={M.dangerHeading} lead={M.dangerLead}>
      {self ? (
        <p className="text-sm text-muted-foreground">{M.selfDelete}</p>
      ) : (
        <Button
          variant="destructive"
          size="sm"
          className="self-start"
          onClick={() => {
            setOpen(true);
          }}
        >
          <Trash2 aria-hidden="true" /> {M.delete}
        </Button>
      )}
      {open ? (
        <DeleteDialog
          user={user}
          onClose={() => {
            setOpen(false);
          }}
        />
      ) : null}
    </Section>
  );
}

/** Type-to-confirm deletion (soft delete; last admin → CONFLICT). */
function DeleteDialog({ user, onClose }: { user: AdminUser; onClose: () => void }) {
  const { withStepUp } = useStepUp();
  const navigate = useNavigate();
  const [del] = useMutation(DeleteUserMutation, {
    refetchQueries: [UsersQuery],
  });
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const inputId = useId();
  const matches = typed.trim().toLowerCase() === user.email.toLowerCase();

  return (
    <Dialog
      open
      onOpenChange={(o) => {
        if (!o && !busy) onClose();
      }}
    >
      <DialogContent className="gap-5 sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{M.deleteTitle}</DialogTitle>
          <DialogDescription>{M.deleteLead(user.email)}</DialogDescription>
        </DialogHeader>
        <form
          noValidate
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (!matches) return;
            setBusy(true);
            setError(undefined);
            withStepUp(() => del({ variables: { id: user.id } }))
              .then(async () => {
                toast.success(M.deleted);
                onClose();
                await navigate("/admin/users");
              })
              .catch((err: unknown) => {
                if (isStepUpCancelled(err)) return;
                setError(hasApiCode(err, ApiCode.conflict) ? M.lastAdmin : userMessage(err));
              })
              .finally(() => {
                setBusy(false);
              });
          }}
        >
          <div className="flex flex-col gap-2">
            <Label htmlFor={inputId}>{M.deleteConfirmLabel(user.email)}</Label>
            <Input
              id={inputId}
              autoComplete="off"
              spellCheck={false}
              value={typed}
              onChange={(e) => {
                setTyped(e.target.value);
              }}
            />
          </div>
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
            <Button type="submit" variant="destructive" disabled={!matches || busy}>
              {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
              {M.delete}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
