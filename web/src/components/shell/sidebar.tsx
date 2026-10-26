import { NavLink } from "react-router";

import { Wordmark } from "@/components/brand/logo";
import { cn } from "@/lib/utils";

import { navForRole, type Role } from "./nav";

export function SidebarNav({ role, onNavigate }: { role: Role; onNavigate?: () => void }) {
  const sections = navForRole(role);
  return (
    <nav aria-label="Main" className="flex flex-col gap-6">
      {sections.map((section) => (
        <div key={section.label} className="flex flex-col gap-1">
          <h2 className="eyebrow px-3 pb-1 text-sidebar-muted-foreground">{section.label}</h2>
          <ul className="flex flex-col gap-px">
            {section.items.map(({ to, label, icon: Icon }) => (
              <li key={to}>
                <NavLink
                  to={to}
                  end={to === "/"}
                  {...(onNavigate ? { onClick: onNavigate } : {})}
                  className={({ isActive }) =>
                    cn(
                      "group relative flex h-8 items-center gap-2.5 rounded-md px-3 text-[0.8125rem] font-medium text-sidebar-muted-foreground transition-colors",
                      "hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
                      isActive &&
                        "bg-sidebar-accent text-sidebar-accent-foreground before:absolute before:inset-y-1.5 before:-left-3 before:w-0.5 before:rounded-full before:bg-primary",
                    )
                  }
                >
                  <Icon aria-hidden="true" className="size-4 shrink-0 opacity-80" />
                  {label}
                </NavLink>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </nav>
  );
}

export function Sidebar({ role }: { role: Role }) {
  return (
    <aside className="sticky top-0 hidden h-dvh w-(--shell-sidebar-width) shrink-0 flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground lg:flex">
      <div className="flex h-(--shell-topbar-height) items-center border-b border-sidebar-border px-5">
        <Wordmark />
      </div>
      <div className="flex-1 overflow-y-auto px-3 py-5">
        <SidebarNav role={role} />
      </div>
      <div className="border-t border-sidebar-border px-5 py-3">
        <p className="eyebrow text-sidebar-muted-foreground">Instance</p>
        <p className="mt-0.5 font-mono text-xs text-sidebar-muted-foreground">
          v{__BEKUP_VERSION__}
        </p>
      </div>
    </aside>
  );
}
