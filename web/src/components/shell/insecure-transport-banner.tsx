import { useQuery } from "@apollo/client/react";
import { ShieldOff } from "lucide-react";

import { pageIsInsecure, ServerInfoQuery } from "@/lib/server-info";
import { cn } from "@/lib/utils";

/**
 * D-095: a permanent, non-dismissable warning while traffic is not encrypted
 * (server.base_url is http, or the page came over plain HTTP).
 */
export function InsecureTransportBanner({ className }: { className?: string }) {
  const { data } = useQuery(ServerInfoQuery, { fetchPolicy: "cache-first" });
  const insecure = data?.serverInfo.insecureTransport === true || pageIsInsecure();
  if (!insecure) return null;
  return (
    <div
      role="alert"
      data-slot="insecure-transport-banner"
      className={cn(
        "flex items-center gap-3 border-b border-destructive bg-destructive px-4 py-2 text-destructive-foreground sm:px-6",
        className,
      )}
    >
      <ShieldOff aria-hidden="true" className="size-4 shrink-0" />
      <p className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-0.5 text-[0.8125rem]">
        <span className="font-mono text-2xs font-semibold tracking-[0.12em] uppercase">
          Not encrypted
        </span>
        <span>
          Connection is not encrypted (no HTTPS). Passwords and session data travel in clear text.
        </span>
      </p>
    </div>
  );
}
