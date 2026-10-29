import { Link, isRouteErrorResponse, useRouteError } from "react-router";

import { LogoMark } from "@/components/brand/logo";
import { Button } from "@/components/ui/button";
import { messages } from "@/lib/messages";

function Frame({ code, title, body }: { code: string; title: string; body: string }) {
  return (
    <div className="ledger-grid flex min-h-dvh items-center justify-center p-6">
      <div className="flex max-w-md flex-col items-start gap-4 rounded-lg border bg-surface p-8 shadow-overlay">
        <LogoMark />
        <p className="eyebrow">{code}</p>
        <h1 className="font-display text-display leading-none">{title}</h1>
        <p className="text-sm text-muted-foreground">{body}</p>
        <Actions />
      </div>
    </div>
  );
}

function Actions() {
  return (
    <div className="flex flex-wrap gap-2">
      <Button asChild variant="outline">
        <Link to="/">{messages.notFound.back}</Link>
      </Button>
      <Button
        variant="ghost"
        onClick={() => {
          window.location.reload();
        }}
      >
        {messages.common.reload}
      </Button>
    </div>
  );
}

/** 404 inside the shell (signed in) or as a full page. */
export function NotFoundPage() {
  return (
    <div className="flex flex-col items-start gap-4 py-16">
      <p className="eyebrow">404</p>
      <h1 className="font-display text-display leading-none">{messages.notFound.title}</h1>
      <p className="text-sm text-muted-foreground">{messages.notFound.body}</p>
      <Button asChild variant="outline">
        <Link to="/">{messages.notFound.back}</Link>
      </Button>
    </div>
  );
}

/**
 * Root error boundary. Shows no error details: messages could contain data
 * the user should not see; details belong in the server logs.
 */
export function RouteErrorPage() {
  const error = useRouteError();
  if (isRouteErrorResponse(error) && error.status === 404) {
    return <Frame code="404" title={messages.notFound.title} body={messages.notFound.body} />;
  }
  return <Frame code="Error" title={messages.routeError.title} body={messages.routeError.body} />;
}

/**
 * Error boundary for the pages inside the shell: the navigation stays
 * usable. A failed lazy chunk (e.g. after an upgrade) asks for a reload.
 */
export function InShellErrorPage() {
  const error = useRouteError();
  if (isRouteErrorResponse(error) && error.status === 404) return <NotFoundPage />;
  return (
    <div role="alert" className="flex flex-col items-start gap-4 py-16">
      <p className="eyebrow">Error</p>
      <h1 className="font-display text-display leading-none">{messages.routeError.title}</h1>
      <p className="max-w-prose text-sm text-muted-foreground">{messages.routeError.body}</p>
      <Actions />
    </div>
  );
}
