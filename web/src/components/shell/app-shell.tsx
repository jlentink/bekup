import { useEffect, useRef } from "react";
import { Outlet, useLocation } from "react-router";

import { useSessionEpoch } from "@/app/session-epoch";
import { useSessionUser } from "@/features/auth/use-session-user";
import { messages } from "@/lib/messages";

import { EnrollmentDeadlineBanner } from "./enrollment-deadline-banner";
import { InsecureTransportBanner } from "./insecure-transport-banner";
import { titleFor } from "./nav";
import { AccountStream } from "./account-stream";
import { RouteProgress } from "./route-progress";
import { Sidebar } from "./sidebar";
import { StreamLimitBanner } from "./stream-limit-banner";
import { TopBar } from "./top-bar";

/**
 * Moves focus to the main region after an in-app navigation (not on the
 * first render), so keyboard and screen reader users start at the new
 * page; keeps the document title in sync.
 */
function useRouteFocus(pathname: string, title: string) {
  const main = useRef<HTMLElement>(null);
  const first = useRef(true);
  useEffect(() => {
    document.title = `${title} · bekup`;
    if (first.current) {
      first.current = false;
      return;
    }
    main.current?.focus({ preventScroll: false });
  }, [pathname, title]);
  return main;
}

export function AppShell() {
  const user = useSessionUser();
  const epoch = useSessionEpoch();
  const { pathname } = useLocation();
  const title = titleFor(pathname);
  const main = useRouteFocus(pathname, title);
  return (
    <div className="flex min-h-dvh">
      <RouteProgress />
      <AccountStream key={epoch} />
      <a
        href="#main"
        className="sr-only z-50 rounded-md bg-primary px-3 py-2 text-primary-foreground focus:not-sr-only focus:fixed focus:top-2 focus:left-2"
      >
        {messages.shell.skipToContent}
      </a>
      <Sidebar role={user.role} />
      <div className="flex min-w-0 flex-1 flex-col">
        <InsecureTransportBanner />
        <EnrollmentDeadlineBanner user={user} />
        <StreamLimitBanner />
        <TopBar user={user} title={title} />
        <main ref={main} id="main" tabIndex={-1} className="flex-1 outline-none">
          <div className="mx-auto w-full max-w-(--content-max-width) px-4 py-8 sm:px-8">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  );
}
