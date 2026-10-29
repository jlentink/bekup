import {
  CircleCheck,
  CircleSlash,
  Clock,
  KeyRound,
  Lock,
  ShieldCheck,
  ShieldOff,
  ShieldQuestion,
  Trash2,
  type LucideIcon,
} from "lucide-react";

import type { AdminUserFieldsFragment, Role } from "@/graphql/gen/graphql";
import { formatDateTime, formatRelative } from "@/lib/format";
import { messages } from "@/lib/messages";
import { cn } from "@/lib/utils";

import { usersMessages as M } from "./messages";
import { accountFlags, twoFactorState, type AccountFlag, type TwoFactorState } from "./status";

const FLAG_META: Record<AccountFlag, { label: string; icon: LucideIcon; tone: string }> = {
  active: {
    label: M.status.active,
    icon: CircleCheck,
    tone: "border-status-success/40 bg-status-success-soft text-status-success-fg",
  },
  disabled: {
    label: M.status.disabled,
    icon: CircleSlash,
    tone: "border-border-strong/60 bg-surface-sunken text-muted-foreground",
  },
  deleted: {
    label: M.status.deleted,
    icon: Trash2,
    tone: "border-border bg-surface-sunken text-muted-foreground line-through decoration-1",
  },
  locked: {
    label: M.status.locked,
    icon: Lock,
    tone: "border-status-failed/40 bg-status-failed-soft text-status-failed-fg",
  },
  temporary: {
    label: M.status.temporary,
    icon: KeyRound,
    tone: "border-status-partial/40 bg-status-partial-soft text-status-partial-fg",
  },
  temporaryExpired: {
    label: M.status.temporaryExpired,
    icon: Clock,
    tone: "border-status-partial/40 bg-status-partial-soft text-status-partial-fg",
  },
};

const chip =
  "inline-flex h-6 items-center gap-1.5 rounded-sm border px-2 text-xs font-medium whitespace-nowrap";

export function AccountFlags({
  user,
  className,
}: {
  user: Parameters<typeof accountFlags>[0];
  className?: string;
}) {
  return (
    <span className={cn("flex flex-wrap gap-1", className)}>
      {accountFlags(user).map((f) => {
        const meta = FLAG_META[f];
        const Icon = meta.icon;
        return (
          <span key={f} data-flag={f} className={cn(chip, meta.tone)}>
            <Icon aria-hidden="true" className="size-3.5 shrink-0" />
            {meta.label}
          </span>
        );
      })}
    </span>
  );
}

const TF_META: Record<TwoFactorState, { icon: LucideIcon; tone: string; hint: string }> = {
  enrolled: { icon: ShieldCheck, tone: "text-status-success-fg", hint: M.twoFactor.enrolledHint },
  grace: { icon: ShieldQuestion, tone: "text-status-partial-fg", hint: M.twoFactor.graceHint },
  overdue: { icon: ShieldOff, tone: "text-status-failed-fg", hint: M.twoFactor.overdueHint },
  none: { icon: ShieldQuestion, tone: "text-muted-foreground", hint: M.twoFactor.noneHint },
};

export function TwoFactorCell({
  user,
}: {
  user: Pick<
    AdminUserFieldsFragment,
    "twoFactorRequired" | "twoFactorEnrollmentDeadline" | "twoFactorEnrolled"
  >;
}) {
  const state = twoFactorState(user);
  const meta = TF_META[state];
  const Icon = meta.icon;
  const label =
    state === "enrolled"
      ? M.twoFactor.enrolled
      : state === "grace"
        ? M.twoFactor.grace(formatRelative(user.twoFactorEnrollmentDeadline))
        : state === "overdue"
          ? M.twoFactor.overdue
          : M.twoFactor.none;
  const hint =
    state === "grace"
      ? `${meta.hint} ${formatDateTime(user.twoFactorEnrollmentDeadline)}`
      : state === "enrolled" && user.twoFactorRequired
        ? M.twoFactor.enrolledRequiredHint
        : meta.hint;
  return (
    <span
      data-two-factor={state}
      className={cn("inline-flex items-center gap-1.5 text-xs", meta.tone)}
      title={hint}
    >
      <Icon aria-hidden="true" className="size-3.5 shrink-0" />
      {label}
    </span>
  );
}

/** When the account was last active (D-184); "Never" without a session on record. */
export function LastSeen({ at, className }: { at: string | null | undefined; className?: string }) {
  if (!at) {
    return (
      <span className={cn("text-muted-foreground", className)} title={M.lastSeen.hint}>
        {M.lastSeen.never}
      </span>
    );
  }
  return (
    <time dateTime={at} className={className} title={`${formatDateTime(at)} · ${M.lastSeen.hint}`}>
      {formatRelative(at)}
    </time>
  );
}

const ROLE_TONE: Record<Role, string> = {
  ADMIN: "border-primary/40 bg-accent text-primary",
  MAINTAINER: "border-border-strong/60 bg-surface text-foreground",
  USER: "border-border bg-surface-sunken text-muted-foreground",
};

/** Mono role tag; the label always carries the role in words. */
export function RoleTag({ role, className }: { role: Role; className?: string }) {
  return (
    <span
      className={cn(
        "inline-flex h-5 items-center rounded-sm border px-1.5 font-mono text-2xs font-medium tracking-[0.08em] uppercase",
        ROLE_TONE[role],
        className,
      )}
    >
      {messages.roles[role]}
    </span>
  );
}

/** Two-letter monogram for an account (decorative). */
export function Monogram({ name, className }: { name: string; className?: string }) {
  const letters =
    name
      .split(/\s+/)
      .filter(Boolean)
      .slice(0, 2)
      .map((p) => p.charAt(0).toUpperCase())
      .join("") || "?";
  return (
    <span
      aria-hidden="true"
      className={cn(
        "inline-flex size-8 shrink-0 items-center justify-center rounded-sm border border-rule bg-surface font-display text-base text-foreground",
        className,
      )}
    >
      {letters}
    </span>
  );
}
