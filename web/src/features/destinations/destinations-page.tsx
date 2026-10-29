import { useQuery } from "@apollo/client/react";
import { HardDrive, Lock, Plus, ShieldCheck } from "lucide-react";
import { Link } from "react-router";

import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { Tag } from "@/components/status/tag";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useSessionUser } from "@/features/auth/use-session-user";
import { LastTestCell } from "@/features/connections/last-test";
import { TableSkeleton } from "@/features/connections/ui";
import type { DestinationsListQuery as ListResult } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { formatRelative } from "@/lib/format";

import { LOCK_LABEL, TYPE_LABEL } from "./labels";
import { DestinationsListQuery } from "./operations";

type Row = ListResult["destinations"][number];

function Modes({ d }: { d: Row }) {
  if (!d.objectLockMode && !d.noDelete)
    return <span className="text-xs text-muted-foreground">Retention by bekup</span>;
  return (
    <div className="flex flex-wrap gap-1.5">
      {d.objectLockMode ? (
        <Tag tone="info" title="Every upload gets this default retention">
          <Lock aria-hidden="true" className="size-3" />
          Object Lock · {LOCK_LABEL[d.objectLockMode]}
          {d.objectLockRetentionDays != null ? ` ${d.objectLockRetentionDays}d` : ""}
        </Tag>
      ) : null}
      {d.noDelete ? (
        <Tag tone="info" title="bekup never deletes here; lifecycle rules handle retention">
          <ShieldCheck aria-hidden="true" className="size-3" />
          No-delete
        </Tag>
      ) : null}
    </div>
  );
}

/** /destinations: where backups are written. */
export function DestinationsPage() {
  const { role } = useSessionUser();
  const isAdmin = role === "ADMIN";
  const { data, loading, error, refetch } = useQuery(DestinationsListQuery, {
    fetchPolicy: "cache-and-network",
  });
  const list = data?.destinations;

  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        eyebrow="Infrastructure"
        title="Destinations"
        description="Where encrypted backups are written: local disk, S3 or SFTP. Credentials are write-only."
        actions={
          isAdmin ? (
            <Button asChild>
              <Link to="/destinations/new">
                <Plus aria-hidden="true" /> New destination
              </Link>
            </Button>
          ) : null
        }
      />
      {!isAdmin ? (
        <p className="text-sm text-muted-foreground">
          Read only: admins manage destinations. You can open one to test it.
        </p>
      ) : null}
      {error && !list ? (
        <Alert variant="destructive">
          <AlertDescription>
            {userMessage(error)}
            <Button variant="outline" size="sm" onClick={() => void refetch()}>
              Try again
            </Button>
          </AlertDescription>
        </Alert>
      ) : !list && loading ? (
        <TableSkeleton label="Loading destinations" />
      ) : list?.length === 0 ? (
        <EmptyState
          icon={HardDrive}
          title="No destinations yet"
          action={
            isAdmin ? (
              <Button asChild variant="outline">
                <Link to="/destinations/new">
                  <Plus aria-hidden="true" /> Add the first destination
                </Link>
              </Button>
            ) : null
          }
        >
          {isAdmin
            ? "Add at least one place to write backups. An S3 bucket with Object Lock keeps backups safe even if bekup itself is compromised."
            : "An admin has not added any destinations yet."}
        </EmptyState>
      ) : list ? (
        <div className="rounded-lg border bg-card">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Protection</TableHead>
                <TableHead>Last test</TableHead>
                <TableHead className="text-right">Updated</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.map((d) => (
                <TableRow key={d.id}>
                  <TableCell className="py-3">
                    <div className="flex flex-col gap-1">
                      <Link
                        to={`/destinations/${d.id}`}
                        className="font-medium underline-offset-4 hover:underline focus-visible:underline"
                      >
                        {d.name}
                      </Link>
                      {d.ready ? null : (
                        <span className="text-xs text-status-failed-fg">
                          Not ready{d.notReadyReason ? `: ${d.notReadyReason}` : ""}
                        </span>
                      )}
                    </div>
                  </TableCell>
                  <TableCell>
                    <span className="rounded-sm border px-1.5 py-0.5 font-mono text-2xs tracking-wider uppercase">
                      {TYPE_LABEL[d.type]}
                    </span>
                  </TableCell>
                  <TableCell>
                    <Modes d={d} />
                  </TableCell>
                  <TableCell>
                    <LastTestCell test={d.lastTest} />
                  </TableCell>
                  <TableCell className="text-right text-xs text-muted-foreground">
                    {formatRelative(d.updatedAt)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      ) : null}
    </div>
  );
}
