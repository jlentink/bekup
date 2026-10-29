import { Bell, Laptop, LockKeyhole, ShieldCheck, UserRound } from "lucide-react";
import { NavLink, Outlet } from "react-router";

import { PageHeader } from "@/components/page-header";
import { ChangePasswordForm } from "@/features/account/change-password-form";
import { Section } from "@/features/account/section";
import { SessionsSection } from "@/features/account/sessions-section";
import { TwoFactorPanel } from "@/features/account/two-factor-panel";
import { useSessionUser } from "@/features/auth/use-session-user";
import { messages } from "@/lib/messages";
import { cn } from "@/lib/utils";

const M = messages.profile;

const TABS = [
  { to: "/profile", label: M.tabs.account, icon: UserRound, end: true },
  { to: "/profile/security", label: M.tabs.security, icon: ShieldCheck, end: false },
  { to: "/profile/sessions", label: M.tabs.sessions, icon: Laptop, end: false },
  { to: "/profile/notifications", label: M.tabs.notifications, icon: Bell, end: false },
] as const;

/** Profile frame: title and the section tabs (links, so each has a URL). */
export function ProfileLayout() {
  const user = useSessionUser();
  return (
    <div className="flex flex-col gap-6">
      <PageHeader eyebrow={M.eyebrow} title={M.title} description={user.email} />
      <nav aria-label={M.title} className="-mt-2 flex gap-1 overflow-x-auto border-b border-rule">
        {TABS.map(({ to, label, icon: Icon, end }) => (
          <NavLink
            key={to}
            to={to}
            end={end}
            className={({ isActive }) =>
              cn(
                "relative -mb-px inline-flex h-10 shrink-0 items-center gap-2 border-b-2 border-transparent px-3 text-[0.8125rem] font-medium text-muted-foreground transition-colors hover:text-foreground",
                isActive && "border-primary text-foreground",
              )
            }
          >
            <Icon aria-hidden="true" className="size-3.5" />
            {label}
          </NavLink>
        ))}
      </nav>
      <div className="max-w-5xl">
        <Outlet />
      </div>
    </div>
  );
}

export function ProfileAccountPage() {
  const user = useSessionUser();
  return (
    <div className="flex flex-col">
      <Section icon={UserRound} title={M.accountHeading}>
        <dl className="grid grid-cols-[auto_1fr] gap-x-8 gap-y-2 text-sm">
          <dt className="eyebrow self-center">Name</dt>
          <dd>{user.displayName}</dd>
          <dt className="eyebrow self-center">E-mail</dt>
          <dd className="font-mono text-[0.8125rem]">{user.email}</dd>
          <dt className="eyebrow self-center">Role</dt>
          <dd>{messages.roles[user.role]}</dd>
        </dl>
      </Section>
      <Section icon={LockKeyhole} title={M.passwordHeading} lead={M.passwordLead}>
        <div className="max-w-sm">
          <ChangePasswordForm account={[user.email, user.displayName]} />
        </div>
      </Section>
    </div>
  );
}

export function ProfileSecurityPage() {
  const user = useSessionUser();
  return <TwoFactorPanel account={user.email} />;
}

export function ProfileSessionsPage() {
  return <SessionsSection />;
}
