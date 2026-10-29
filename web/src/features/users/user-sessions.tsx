import { useMutation, useQuery } from "@apollo/client/react";
import { Laptop, MonitorSmartphone } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Section } from "@/features/account/section";
import { describeUserAgent } from "@/features/account/model";
import { reportActionError, useStepUp } from "@/features/auth/use-step-up";
import { userMessage } from "@/lib/api-error";
import { formatDateTime, formatRelative } from "@/lib/format";
import { messages } from "@/lib/messages";

import { usersMessages as M } from "./messages";
import { RevokeUserSessionsMutation, UserSessionsQuery } from "./operations";

/** Another account's active sessions, with "revoke all" (step-up). */
export function UserSessionsSection({ userId, readOnly }: { userId: string; readOnly: boolean }) {
  const { withStepUp } = useStepUp();
  const { data, error, refetch } = useQuery(UserSessionsQuery, {
    variables: { userId },
    fetchPolicy: "cache-and-network",
  });
  const [revokeAll] = useMutation(RevokeUserSessionsMutation);
  const [confirming, setConfirming] = useState(false);
  const sessions = data?.userSessions ?? [];
  const latest = sessions.reduce<string | null>(
    (acc, s) => (acc === null || s.lastSeenAt > acc ? s.lastSeenAt : acc),
    null,
  );

  return (
    <Section
      icon={Laptop}
      title={M.sessionsHeading}
      lead={M.sessionsLead}
      status={
        data ? (
          <span className="font-mono text-2xs text-muted-foreground" title={formatDateTime(latest)}>
            {latest ? M.lastActive(formatRelative(latest)) : M.neverActive}
          </span>
        ) : null
      }
    >
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
        </div>
      ) : sessions.length === 0 ? (
        <p className="text-sm text-muted-foreground">{M.noSessions}</p>
      ) : (
        <ul
          aria-label={M.sessionsHeading}
          className="divide-y divide-rule rounded-md border bg-surface px-4 py-1"
        >
          {sessions.map((s) => {
            const Icon = /Mobile|Android|iPhone/.test(s.userAgent ?? "")
              ? MonitorSmartphone
              : Laptop;
            return (
              <li key={s.id} className="flex items-center gap-3 py-2.5">
                <Icon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
                <div className="flex min-w-0 flex-col gap-0.5">
                  <span className="text-sm font-medium">{describeUserAgent(s.userAgent)}</span>
                  <span className="flex flex-wrap gap-x-3 font-mono text-2xs text-muted-foreground">
                    {s.ip ? <span>{s.ip}</span> : null}
                    <span title={formatDateTime(s.lastSeenAt)}>
                      {messages.sessions.lastSeen(formatRelative(s.lastSeenAt))}
                    </span>
                    <span>{messages.sessions.aal(s.aal)}</span>
                  </span>
                </div>
              </li>
            );
          })}
        </ul>
      )}
      {!readOnly && sessions.length > 0 ? (
        <Button
          variant="outline"
          size="sm"
          className="self-start"
          onClick={() => {
            setConfirming(true);
          }}
        >
          {M.revokeAll}
        </Button>
      ) : null}
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={M.revokeAllTitle}
        description={M.revokeAllLead}
        confirmLabel={M.revokeAll}
        destructive
        onConfirm={async () => {
          try {
            const res = await withStepUp(() => revokeAll({ variables: { userId } }));
            toast.success(M.revokedCount(res.data?.revokeUserSessions ?? 0));
          } catch (err) {
            reportActionError(err);
            throw err;
          }
          await refetch().catch(() => undefined);
        }}
      />
    </Section>
  );
}
