import { Link, isRouteErrorResponse, useRouteError } from "react-router";

import { LogoMark } from "@/components/brand/logo";
import { Button } from "@/components/ui/button";

function Frame({ code, title, body }: { code: string; title: string; body: string }) {
  return (
    <div className="ledger-grid flex min-h-dvh items-center justify-center p-6">
      <div className="flex max-w-md flex-col items-start gap-4 rounded-lg border bg-surface p-8 shadow-overlay">
        <LogoMark />
        <p className="eyebrow">{code}</p>
        <h1 className="font-display text-display leading-none">{title}</h1>
        <p className="text-sm text-muted-foreground">{body}</p>
        <Button asChild variant="outline">
          <Link to="/">Back to the dashboard</Link>
        </Button>
      </div>
    </div>
  );
}

export function NotFoundPage() {
  return <Frame code="404" title="Nothing filed here" body="That page does not exist." />;
}

/**
 * Route error boundary. Shows no error details: messages could contain data
 * the user should not see; details belong in the server logs.
 */
export function RouteErrorPage() {
  const error = useRouteError();
  if (isRouteErrorResponse(error) && error.status === 404) return <NotFoundPage />;
  return (
    <Frame
      code="Error"
      title="Something went wrong"
      body="The page failed to load. Reload, or go back to the dashboard."
    />
  );
}
