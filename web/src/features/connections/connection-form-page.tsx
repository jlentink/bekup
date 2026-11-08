import { skipToken, useQuery } from "@apollo/client/react";
import { ArrowLeft, Lock } from "lucide-react";
import { Link, useParams } from "react-router";

import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { useSessionUser } from "@/features/auth/use-session-user";
import { userMessage } from "@/lib/api-error";
import { defineMessages } from "@/lib/i18n";

import { ConnectionForm } from "./connection-form";
import { ConnectionDetailQuery } from "./operations";
import { DetailSkeleton } from "./ui";

const T = defineMessages({
  en: {
    adminsOnly: "Admins only",
    back: "Back",
    adminsOnlyBody: "Only admins create and change connections.",
    connections: "Connections",
    eyebrow: "Infrastructure",
    newTitle: "New connection",
    newDescription:
      "After creating it, confirm the server's certificate (or the bastion's host key) on the connection page.",
    connection: "Connection",
    editEyebrow: "Edit connection",
    loading: "Loading connection",
    notFound: "Connection not found",
    notFoundBody: "It was deleted or never existed.",
  },
  nl: {
    adminsOnly: "Alleen beheerders",
    back: "Terug",
    adminsOnlyBody: "Alleen beheerders maken en wijzigen verbindingen.",
    connections: "Verbindingen",
    eyebrow: "Infrastructuur",
    newTitle: "Nieuwe verbinding",
    newDescription:
      "Bevestig na het aanmaken het certificaat van de server (of de hostsleutel van de bastionhost) op de pagina van de verbinding.",
    connection: "Verbinding",
    editEyebrow: "Verbinding bewerken",
    loading: "Verbinding laden",
    notFound: "Verbinding niet gevonden",
    notFoundBody: "Ze is verwijderd of heeft nooit bestaan.",
  },
});

function AdminsOnly({ back }: { back: string }) {
  return (
    <EmptyState
      icon={Lock}
      title={T.adminsOnly}
      action={
        <Button asChild variant="outline">
          <Link to={back}>{T.back}</Link>
        </Button>
      }
    >
      {T.adminsOnlyBody}
    </EmptyState>
  );
}

/** /connections/new */
export function NewConnectionPage() {
  const { role } = useSessionUser();
  return (
    <div className="flex flex-col gap-6">
      <Button asChild variant="ghost" size="sm" className="self-start">
        <Link to="/connections">
          <ArrowLeft aria-hidden="true" /> {T.connections}
        </Link>
      </Button>
      <PageHeader eyebrow={T.eyebrow} title={T.newTitle} description={T.newDescription} />
      {role === "ADMIN" ? <ConnectionForm /> : <AdminsOnly back="/connections" />}
    </div>
  );
}

/** /connections/:id/edit */
export function EditConnectionPage() {
  const { id = "" } = useParams();
  const { role } = useSessionUser();
  const { data, loading, error } = useQuery(
    ConnectionDetailQuery,
    role === "ADMIN" ? { variables: { id }, fetchPolicy: "network-only" } : skipToken,
  );
  const c = data?.connection;
  return (
    <div className="flex flex-col gap-6">
      <Button asChild variant="ghost" size="sm" className="self-start">
        <Link to={`/connections/${id}`}>
          <ArrowLeft aria-hidden="true" /> {c?.name ?? T.connection}
        </Link>
      </Button>
      <PageHeader eyebrow={T.editEyebrow} title={c?.name ?? T.connection} />
      {role !== "ADMIN" ? (
        <AdminsOnly back={`/connections/${id}`} />
      ) : loading && !c ? (
        <DetailSkeleton label={T.loading} />
      ) : error && !c ? (
        <Alert variant="destructive">
          <AlertDescription>{userMessage(error)}</AlertDescription>
        </Alert>
      ) : c ? (
        <ConnectionForm key={c.updatedAt} original={c} />
      ) : (
        <EmptyState icon={Lock} title={T.notFound}>
          {T.notFoundBody}
        </EmptyState>
      )}
    </div>
  );
}
