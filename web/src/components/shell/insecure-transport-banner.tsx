import { skipToken, useQuery } from "@apollo/client/react";
import { ShieldOff } from "lucide-react";

import { formatDateTime } from "@/lib/format";
import { messages } from "@/lib/messages";
import { SecurityRisksQuery } from "@/lib/operations";
import { pageIsInsecure, ServerInfoQuery } from "@/lib/server-info";
import { cn } from "@/lib/utils";

/**
 * D-095: a permanent, non-dismissable warning while traffic is not encrypted
 * (server.base_url is http, or the page came over plain HTTP). Signed in, an
 * accepted risk (D-228) is named, but the warning stays: it concerns every
 * user's own password and session.
 */
export function InsecureTransportBanner({
  className,
  signedIn = false,
}: {
  className?: string;
  signedIn?: boolean;
}) {
  const { data } = useQuery(ServerInfoQuery, { fetchPolicy: "cache-first" });
  const insecure = data?.serverInfo.insecureTransport === true || pageIsInsecure();
  const risks = useQuery(
    SecurityRisksQuery,
    signedIn && insecure ? { fetchPolicy: "cache-first" } : skipToken,
  );
  if (!insecure) return null;
  const accepted = risks.data?.securityRisks.find(
    (r) => r.kind === "INSECURE_TRANSPORT" && r.acceptance,
  )?.acceptance;
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
          {messages.insecureTransport.label}
        </span>
        <span>{messages.insecureTransport.body}</span>
        {accepted ? (
          <span data-testid="insecure-transport-accepted" className="text-2xs opacity-85">
            {accepted.expiresAt
              ? messages.risks.bannerUntil(formatDateTime(accepted.expiresAt))
              : messages.risks.bannerForever}
          </span>
        ) : null}
      </p>
    </div>
  );
}
