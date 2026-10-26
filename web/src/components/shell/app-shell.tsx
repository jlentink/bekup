import { Outlet, useLocation } from "react-router";

import { PLACEHOLDER_USER } from "@/lib/session";

import { allNavItems } from "./nav";
import { Sidebar } from "./sidebar";
import { TopBar } from "./top-bar";

function titleFor(pathname: string): string {
  const match = allNavItems()
    .filter((item) => (item.to === "/" ? pathname === "/" : pathname.startsWith(item.to)))
    .sort((a, b) => b.to.length - a.to.length)[0];
  return match?.label ?? "bekup";
}

export function AppShell() {
  const user = PLACEHOLDER_USER;
  const { pathname } = useLocation();
  return (
    <div className="flex min-h-dvh">
      <a
        href="#main"
        className="sr-only z-50 rounded-md bg-primary px-3 py-2 text-primary-foreground focus:not-sr-only focus:fixed focus:top-2 focus:left-2"
      >
        Skip to content
      </a>
      <Sidebar role={user.role} />
      <div className="flex min-w-0 flex-1 flex-col">
        <TopBar user={user} title={titleFor(pathname)} />
        <main id="main" tabIndex={-1} className="flex-1 outline-none">
          <div className="mx-auto w-full max-w-(--content-max-width) px-4 py-8 sm:px-8">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  );
}
