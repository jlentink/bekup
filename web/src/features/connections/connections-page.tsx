import { useQuery } from "@apollo/client/react";
import { Cable, Network, Plus } from "lucide-react";
import { Link } from "react-router";

import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { SecurityBadge } from "@/components/status/security-badge";
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
import type { ConnectionsListQuery as ListResult } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";

import { ENGINE_LABEL, pinStatusMeta, TLS_MODE } from "./labels";
import { LastTestCell, PrivilegeVerdictCell } from "./last-test";
import { ConnectionsListQuery } from "./operations";
import { TableSkeleton } from "./ui";

type Row = ListResult["connections"][number];

function TransportCell({ c }: { c: Row }) {
  const pin = pinStatusMeta(c.pinStatus, c.pinExpiresAt);
  return (
    <div className="flex flex-col items-start gap-1.5">
      <span className="text-sm">{TLS_MODE[c.tlsMode].short}</span>
      {c.insecureTransport ? (
        <SecurityBadge flag="insecure-transport" />
      ) : (
        <Tag tone={pin.tone} {...(pin.detail ? { title: pin.detail } : {})}>
          {pin.label}
        </Tag>
      )}
    </div>
  );
}

function ConnectionRow({ c }: { c: Row }) {
  return (
    <TableRow>
      <TableCell className="py-3">
        <div className="flex flex-col gap-1">
          <Link
            to={`/connections/${c.id}`}
            className="font-medium underline-offset-4 hover:underline focus-visible:underline"
          >
            {c.name}
          </Link>
          {c.ready ? null : (
            <span className="text-xs text-status-failed-fg">
              Not ready{c.notReadyReason ? `: ${c.notReadyReason}` : ""}
            </span>
          )}
        </div>
      </TableCell>
      <TableCell>
        <div className="flex flex-col gap-0.5">
          <span>{ENGINE_LABEL[c.engine]}</span>
          <span className="font-mono text-xs text-muted-foreground">
            {c.versionKey}
            {c.legacyVersion ? " · legacy" : ""}
          </span>
        </div>
      </TableCell>
      <TableCell className="font-mono text-xs">
        {c.host}:{c.port}
      </TableCell>
      <TableCell>
        <TransportCell c={c} />
      </TableCell>
      <TableCell>
        {c.sshEnabled ? (
          <span className="inline-flex items-center gap-1.5 text-sm">
            <Network aria-hidden="true" className="size-3.5 text-muted-foreground" /> Tunnel
          </span>
        ) : (
          <span className="text-xs text-muted-foreground">Direct</span>
        )}
      </TableCell>
      <TableCell>
        <LastTestCell test={c.lastTest} />
      </TableCell>
      <TableCell>
        <PrivilegeVerdictCell test={c.lastTest} />
      </TableCell>
      <TableCell className="text-right font-mono text-xs">
        <span title="Effective concurrent runs">{c.effectiveMaxConcurrentRuns}</span>
        <span className="block text-muted-foreground">
          {c.maxConcurrentRuns == null ? "default" : `set ${c.maxConcurrentRuns}`}
        </span>
      </TableCell>
    </TableRow>
  );
}

/** /connections: every connection with its transport, tunnel and run limit. */
export function ConnectionsPage() {
  const { role } = useSessionUser();
  const isAdmin = role === "ADMIN";
  const { data, loading, error, refetch } = useQuery(ConnectionsListQuery, {
    fetchPolicy: "cache-and-network",
  });
  const list = data?.connections;

  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        eyebrow="Infrastructure"
        title="Connections"
        description="The databases bekup backs up. Credentials are write-only, and TLS is pinned to a certificate you confirmed unless an admin opted out."
        actions={
          isAdmin ? (
            <Button asChild>
              <Link to="/connections/new">
                <Plus aria-hidden="true" /> New connection
              </Link>
            </Button>
          ) : null
        }
      />
      {!isAdmin ? (
        <p className="text-sm text-muted-foreground">
          Read only: admins manage connections. You can open a connection to test it or list its
          databases.
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
        <TableSkeleton label="Loading connections" />
      ) : list?.length === 0 ? (
        <EmptyState
          icon={Cable}
          title="No connections yet"
          action={
            isAdmin ? (
              <Button asChild variant="outline">
                <Link to="/connections/new">
                  <Plus aria-hidden="true" /> Add the first connection
                </Link>
              </Button>
            ) : null
          }
        >
          {isAdmin
            ? "Add a database: pick its engine and version, enter a backup user, then confirm the server's certificate (or set up an SSH tunnel)."
            : "An admin has not added any databases yet."}
        </EmptyState>
      ) : list ? (
        <div className="rounded-lg border bg-card">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Engine</TableHead>
                <TableHead>Host</TableHead>
                <TableHead>Transport</TableHead>
                <TableHead>SSH</TableHead>
                <TableHead>Last test</TableHead>
                <TableHead>Role</TableHead>
                <TableHead className="text-right">Runs</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.map((c) => (
                <ConnectionRow key={c.id} c={c} />
              ))}
            </TableBody>
          </Table>
        </div>
      ) : null}
    </div>
  );
}
