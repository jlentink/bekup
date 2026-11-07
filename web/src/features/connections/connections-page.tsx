import { useQuery } from "@apollo/client/react";
import { Cable, Network, Plus } from "lucide-react";
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
import { RiskBadge } from "@/features/accepted-risks/risk-badge";
import type { ConnectionsListQuery as ListResult } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { defineMessages } from "@/lib/i18n";

import { ENGINE_LABEL, pinStatusMeta, TLS_MODE } from "./labels";
import { LastTestCell, PrivilegeVerdictCell } from "./last-test";
import { ConnectionsListQuery } from "./operations";
import { TableSkeleton } from "./ui";

const T = defineMessages({
  en: {
    notReady: (reason: string | null | undefined) => `Not ready${reason ? `: ${reason}` : ""}`,
    legacy: " · legacy",
    sshTunnel: "SSH tunnel",
    direct: "Direct",
    effectiveRuns: "Effective concurrent runs",
    runsDefault: "default",
    runsSet: (n: number) => `set ${n}`,
    eyebrow: "Infrastructure",
    title: "Connections",
    description:
      "The databases bekup backs up. Credentials are write-only, and TLS is pinned to a certificate you confirmed unless an admin opted out.",
    newConnection: "New connection",
    readOnly:
      "Read only: admins manage connections. You can open a connection to test it or list its databases.",
    retry: "Try again",
    loading: "Loading connections",
    empty: "No connections yet",
    addFirst: "Add the first connection",
    emptyAdmin:
      "Add a database: pick its engine and version, enter a backup user, then confirm the server's certificate (or set up an SSH tunnel).",
    emptyOther: "An admin has not added any databases yet.",
    cols: {
      name: "Name",
      engine: "Engine",
      host: "Host",
      transport: "Transport",
      connection: "Connection",
      lastTest: "Last test",
      role: "Role",
      runs: "Runs",
    },
  },
  nl: {
    notReady: (reason: string | null | undefined) => `Niet gereed${reason ? `: ${reason}` : ""}`,
    legacy: " · verouderd",
    sshTunnel: "SSH-tunnel",
    direct: "Direct",
    effectiveRuns: "Gelijktijdige runs die gelden",
    runsDefault: "standaard",
    runsSet: (n: number) => `ingesteld ${n}`,
    eyebrow: "Infrastructuur",
    title: "Verbindingen",
    description:
      "De databases waarvan bekup back-ups maakt. Inloggegevens zijn alleen-schrijven en TLS is vastgepind op een certificaat dat je hebt bevestigd, tenzij een beheerder daarvan heeft afgezien.",
    newConnection: "Nieuwe verbinding",
    readOnly:
      "Alleen lezen: beheerders beheren verbindingen. Je kunt een verbinding openen om haar te testen of haar databases te tonen.",
    retry: "Opnieuw proberen",
    loading: "Verbindingen laden",
    empty: "Nog geen verbindingen",
    addFirst: "De eerste verbinding toevoegen",
    emptyAdmin:
      "Voeg een database toe: kies de engine en versie, vul een back-upgebruiker in en bevestig daarna het certificaat van de server (of stel een SSH-tunnel in).",
    emptyOther: "Een beheerder heeft nog geen databases toegevoegd.",
    cols: {
      name: "Naam",
      engine: "Engine",
      host: "Host",
      transport: "Transport",
      connection: "Verbinding",
      lastTest: "Laatste test",
      role: "Rol",
      runs: "Runs",
    },
  },
});

type Row = ListResult["connections"][number];

function TransportCell({ c }: { c: Row }) {
  const pin = pinStatusMeta(c.pinStatus, c.pinExpiresAt);
  return (
    <div className="flex flex-col items-start gap-1.5">
      <span className="text-sm">{TLS_MODE[c.tlsMode].short}</span>
      {c.insecureTransport ? (
        <RiskBadge
          flag="insecure-transport"
          kind="CONNECTION_TLS"
          subjectId={c.id}
          showAction={false}
        />
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
            <span className="text-xs text-status-failed-fg">{T.notReady(c.notReadyReason)}</span>
          )}
        </div>
      </TableCell>
      <TableCell>
        <div className="flex flex-col gap-0.5">
          <span>{ENGINE_LABEL[c.engine]}</span>
          <span className="font-mono text-xs text-muted-foreground">
            {c.versionKey}
            {c.legacyVersion ? T.legacy : ""}
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
            <Network aria-hidden="true" className="size-3.5 text-muted-foreground" /> {T.sshTunnel}
          </span>
        ) : (
          <span className="text-sm">{T.direct}</span>
        )}
      </TableCell>
      <TableCell>
        <LastTestCell test={c.lastTest} />
      </TableCell>
      <TableCell>
        <PrivilegeVerdictCell test={c.lastTest} />
      </TableCell>
      <TableCell className="text-right font-mono text-xs">
        <span title={T.effectiveRuns}>{c.effectiveMaxConcurrentRuns}</span>
        <span className="block text-muted-foreground">
          {c.maxConcurrentRuns == null ? T.runsDefault : T.runsSet(c.maxConcurrentRuns)}
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
        eyebrow={T.eyebrow}
        title={T.title}
        description={T.description}
        actions={
          isAdmin ? (
            <Button asChild>
              <Link to="/connections/new">
                <Plus aria-hidden="true" /> {T.newConnection}
              </Link>
            </Button>
          ) : null
        }
      />
      {!isAdmin ? <p className="text-sm text-muted-foreground">{T.readOnly}</p> : null}
      {error && !list ? (
        <Alert variant="destructive">
          <AlertDescription>
            {userMessage(error)}
            <Button variant="outline" size="sm" onClick={() => void refetch()}>
              {T.retry}
            </Button>
          </AlertDescription>
        </Alert>
      ) : !list && loading ? (
        <TableSkeleton label={T.loading} />
      ) : list?.length === 0 ? (
        <EmptyState
          icon={Cable}
          title={T.empty}
          action={
            isAdmin ? (
              <Button asChild variant="outline">
                <Link to="/connections/new">
                  <Plus aria-hidden="true" /> {T.addFirst}
                </Link>
              </Button>
            ) : null
          }
        >
          {isAdmin ? T.emptyAdmin : T.emptyOther}
        </EmptyState>
      ) : list ? (
        <div className="rounded-lg border bg-card">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{T.cols.name}</TableHead>
                <TableHead>{T.cols.engine}</TableHead>
                <TableHead>{T.cols.host}</TableHead>
                <TableHead>{T.cols.transport}</TableHead>
                <TableHead>{T.cols.connection}</TableHead>
                <TableHead>{T.cols.lastTest}</TableHead>
                <TableHead>{T.cols.role}</TableHead>
                <TableHead className="text-right">{T.cols.runs}</TableHead>
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
