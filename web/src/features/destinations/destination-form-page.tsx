import { skipToken, useQuery } from "@apollo/client/react";
import { ArrowLeft, Lock } from "lucide-react";
import { Link, useParams } from "react-router";

import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { useSessionUser } from "@/features/auth/use-session-user";
import { DetailSkeleton } from "@/features/connections/ui";
import { userMessage } from "@/lib/api-error";
import { defineMessages } from "@/lib/i18n";

import { DestinationForm } from "./destination-form";
import { DestinationDetailQuery } from "./operations";

const T = defineMessages({
  en: {
    adminsOnly: "Admins only",
    back: "Back",
    adminsOnlyBody: "Only admins create and change destinations.",
    destinations: "Destinations",
    eyebrow: "Infrastructure",
    newTitle: "New destination",
    destination: "Destination",
    editEyebrow: "Edit destination",
    loading: "Loading destination",
    notFound: "Destination not found",
    notFoundBody: "It was deleted or never existed.",
  },
  nl: {
    adminsOnly: "Alleen voor beheerders",
    back: "Terug",
    adminsOnlyBody: "Alleen beheerders maken en wijzigen bestemmingen.",
    destinations: "Bestemmingen",
    eyebrow: "Infrastructuur",
    newTitle: "Nieuwe bestemming",
    destination: "Bestemming",
    editEyebrow: "Bestemming bewerken",
    loading: "Bestemming laden",
    notFound: "Bestemming niet gevonden",
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

/** /destinations/new */
export function NewDestinationPage() {
  const { role } = useSessionUser();
  return (
    <div className="flex flex-col gap-6">
      <Button asChild variant="ghost" size="sm" className="self-start">
        <Link to="/destinations">
          <ArrowLeft aria-hidden="true" /> {T.destinations}
        </Link>
      </Button>
      <PageHeader eyebrow={T.eyebrow} title={T.newTitle} />
      {role === "ADMIN" ? <DestinationForm /> : <AdminsOnly back="/destinations" />}
    </div>
  );
}

/** /destinations/:id/edit */
export function EditDestinationPage() {
  const { id = "" } = useParams();
  const { role } = useSessionUser();
  const { data, loading, error } = useQuery(
    DestinationDetailQuery,
    role === "ADMIN" ? { variables: { id }, fetchPolicy: "network-only" } : skipToken,
  );
  const d = data?.destination;
  return (
    <div className="flex flex-col gap-6">
      <Button asChild variant="ghost" size="sm" className="self-start">
        <Link to={`/destinations/${id}`}>
          <ArrowLeft aria-hidden="true" /> {d?.name ?? T.destination}
        </Link>
      </Button>
      <PageHeader eyebrow={T.editEyebrow} title={d?.name ?? T.destination} />
      {role !== "ADMIN" ? (
        <AdminsOnly back={`/destinations/${id}`} />
      ) : loading && !d ? (
        <DetailSkeleton label={T.loading} />
      ) : error && !d ? (
        <Alert variant="destructive">
          <AlertDescription>{userMessage(error)}</AlertDescription>
        </Alert>
      ) : d ? (
        <DestinationForm key={d.updatedAt} original={d} />
      ) : (
        <EmptyState icon={Lock} title={T.notFound}>
          {T.notFoundBody}
        </EmptyState>
      )}
    </div>
  );
}
