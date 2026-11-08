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
import { defineMessages } from "@/lib/i18n";

import { StoredCell } from "./destination-storage";
import { LOCK_LABEL, TYPE_LABEL } from "./labels";
import { DestinationsListQuery } from "./operations";
import { ST } from "./storage-text";
import { useStoredByDestination } from "./use-stored";

type Row = ListResult["destinations"][number];

const T = defineMessages({
  en: {
    retentionByBekup: "Retention by bekup",
    lockTitle: "Every upload gets this default retention",
    lockTag: (mode: string) => `Object Lock · ${mode}`,
    lockTagDays: (mode: string, days: number) => `Object Lock · ${mode} ${days}d`,
    noDeleteTitle: "bekup never deletes here; lifecycle rules handle retention",
    noDelete: "No-delete",
    eyebrow: "Infrastructure",
    title: "Destinations",
    description:
      "Where encrypted backups are written: local disk, S3, SFTP or WebDAV. Credentials are write-only.",
    newDestination: "New destination",
    readOnly: "Read only: admins manage destinations. You can open one to test it.",
    retry: "Try again",
    loading: "Loading destinations",
    empty: "No destinations yet",
    addFirst: "Add the first destination",
    emptyAdmin:
      "Add at least one place to write backups. An S3 bucket with Object Lock keeps backups safe even if bekup itself is compromised.",
    emptyOther: "An admin has not added any destinations yet.",
    name: "Name",
    type: "Type",
    protection: "Protection",
    lastTest: "Last test",
    updated: "Updated",
    notReady: "Not ready",
    notReadyReason: (reason: string) => `Not ready: ${reason}`,
  },
  nl: {
    retentionByBekup: "Bewaarbeleid door bekup",
    lockTitle: "Elke upload krijgt deze standaardbewaartermijn",
    lockTag: (mode: string) => `Object Lock · ${mode}`,
    lockTagDays: (mode: string, days: number) => `Object Lock · ${mode} ${days}d`,
    noDeleteTitle: "bekup verwijdert hier nooit iets; lifecycle-regels regelen het bewaarbeleid",
    noDelete: "Niet verwijderen",
    eyebrow: "Infrastructuur",
    title: "Bestemmingen",
    description:
      "Waar versleutelde back-ups worden geschreven: lokale schijf, S3, SFTP of WebDAV. Inloggegevens zijn alleen-schrijven.",
    newDestination: "Nieuwe bestemming",
    readOnly:
      "Alleen lezen: beheerders beheren bestemmingen. Je kunt er een openen om hem te testen.",
    retry: "Opnieuw proberen",
    loading: "Bestemmingen laden",
    empty: "Nog geen bestemmingen",
    addFirst: "De eerste bestemming toevoegen",
    emptyAdmin:
      "Voeg minstens één plek toe om back-ups naartoe te schrijven. Een S3-bucket met Object Lock houdt back-ups veilig, zelfs als bekup zelf gecompromitteerd is.",
    emptyOther: "Een beheerder heeft nog geen bestemmingen toegevoegd.",
    name: "Naam",
    type: "Type",
    protection: "Bescherming",
    lastTest: "Laatste test",
    updated: "Bijgewerkt",
    notReady: "Niet gereed",
    notReadyReason: (reason: string) => `Niet gereed: ${reason}`,
  },
});

function Modes({ d }: { d: Row }) {
  if (!d.objectLockMode && !d.noDelete)
    return <span className="text-xs text-muted-foreground">{T.retentionByBekup}</span>;
  return (
    <div className="flex flex-wrap gap-1.5">
      {d.objectLockMode ? (
        <Tag tone="info" title={T.lockTitle}>
          <Lock aria-hidden="true" className="size-3" />
          {d.objectLockRetentionDays != null
            ? T.lockTagDays(LOCK_LABEL[d.objectLockMode], d.objectLockRetentionDays)
            : T.lockTag(LOCK_LABEL[d.objectLockMode])}
        </Tag>
      ) : null}
      {d.noDelete ? (
        <Tag tone="info" title={T.noDeleteTitle}>
          <ShieldCheck aria-hidden="true" className="size-3" />
          {T.noDelete}
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
  const stored = useStoredByDestination();

  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        eyebrow={T.eyebrow}
        title={T.title}
        description={T.description}
        actions={
          isAdmin ? (
            <Button asChild>
              <Link to="/destinations/new">
                <Plus aria-hidden="true" /> {T.newDestination}
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
          icon={HardDrive}
          title={T.empty}
          action={
            isAdmin ? (
              <Button asChild variant="outline">
                <Link to="/destinations/new">
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
                <TableHead>{T.name}</TableHead>
                <TableHead>{T.type}</TableHead>
                <TableHead>{T.protection}</TableHead>
                <TableHead>{T.lastTest}</TableHead>
                <TableHead className="text-right">{ST.column}</TableHead>
                <TableHead className="text-right">{T.updated}</TableHead>
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
                          {d.notReadyReason ? T.notReadyReason(d.notReadyReason) : T.notReady}
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
                  <TableCell className="text-right">
                    {stored.failed ? null : (
                      <StoredCell row={stored.byId.get(d.id)} loaded={stored.loaded} />
                    )}
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
