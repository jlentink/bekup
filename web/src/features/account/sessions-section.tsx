import { useApolloClient, useMutation, useQuery } from "@apollo/client/react";
import { Laptop, MonitorSmartphone } from "lucide-react";
import { useState } from "react";
import { useNavigate } from "react-router";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { reportActionError } from "@/features/auth/use-step-up";
import type { MySessionsQuery as Sessions } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { formatDateTime, formatRelative } from "@/lib/format";
import { messages } from "@/lib/messages";
import { cn } from "@/lib/utils";

import { MySessionsQuery, RevokeSessionMutation } from "./operations";
import { Section } from "./section";
import { describeUserAgent } from "./model";

const M = messages.sessions;

type Session = Sessions["mySessions"][number];

function SessionRow({ session, onRevoke }: { session: Session; onRevoke: () => void }) {
  const Icon = /Mobile|Android|iPhone/.test(session.userAgent ?? "") ? MonitorSmartphone : Laptop;
  return (
    <li
      className={cn(
        "flex flex-col gap-2 py-3 first:pt-0 last:pb-0 sm:flex-row sm:items-center sm:gap-4",
      )}
    >
      <Icon aria-hidden="true" className="hidden size-4 shrink-0 text-muted-foreground sm:block" />
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <p className="flex flex-wrap items-center gap-2 font-medium">
          {describeUserAgent(session.userAgent)}
          {session.current ? (
            <span className="inline-flex h-5 items-center rounded-sm bg-accent px-1.5 font-mono text-2xs tracking-[0.08em] text-primary uppercase">
              {M.current}
            </span>
          ) : null}
        </p>
        <p className="flex flex-wrap gap-x-3 gap-y-0.5 font-mono text-2xs text-muted-foreground">
          {session.ip ? <span>{session.ip}</span> : null}
          <span title={formatDateTime(session.lastSeenAt)}>
            {M.lastSeen(formatRelative(session.lastSeenAt))}
          </span>
          <span title={formatDateTime(session.createdAt)}>
            {M.signedIn(formatRelative(session.createdAt))}
          </span>
          <span>{M.aal(session.aal)}</span>
        </p>
      </div>
      <Button variant="ghost" size="sm" className="self-start sm:self-auto" onClick={onRevoke}>
        {session.current ? messages.common.signOut : M.revoke}
      </Button>
    </li>
  );
}

/** The own sessions (current first), with revoke. */
export function SessionsSection() {
  const navigate = useNavigate();
  const client = useApolloClient();
  const { data, error, refetch } = useQuery(MySessionsQuery, { fetchPolicy: "cache-and-network" });
  const [revoke] = useMutation(RevokeSessionMutation);
  const [target, setTarget] = useState<Session | null>(null);

  const sessions = [...(data?.mySessions ?? [])].sort(
    (a, b) => Number(b.current) - Number(a.current),
  );

  return (
    <Section icon={Laptop} title={M.heading} lead={M.lead}>
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
        <div className="flex flex-col gap-2" aria-busy="true">
          <Skeleton className="h-12 w-full" />
          <Skeleton className="h-12 w-full" />
        </div>
      ) : (
        <ul
          aria-label={M.heading}
          className="divide-y divide-rule rounded-md border bg-surface px-4 py-3"
        >
          {sessions.map((s) => (
            <SessionRow
              key={s.id}
              session={s}
              onRevoke={() => {
                setTarget(s);
              }}
            />
          ))}
        </ul>
      )}
      <ConfirmDialog
        open={target !== null}
        onOpenChange={(o) => {
          if (!o) setTarget(null);
        }}
        title={target?.current ? M.revokeCurrentTitle : M.revokeTitle}
        description={M.revokeLead}
        confirmLabel={target?.current ? messages.common.signOut : M.revoke}
        destructive
        onConfirm={async () => {
          if (!target) return;
          try {
            await revoke({ variables: { id: target.id } });
          } catch (err) {
            reportActionError(err);
            throw err;
          }
          if (target.current) {
            await client.clearStore();
            await navigate("/login", { replace: true });
            return;
          }
          toast.success(M.revoked);
          await refetch().catch(() => undefined);
        }}
      />
    </Section>
  );
}
