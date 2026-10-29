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

import { DestinationForm } from "./destination-form";
import { DestinationDetailQuery } from "./operations";

function AdminsOnly({ back }: { back: string }) {
  return (
    <EmptyState
      icon={Lock}
      title="Admins only"
      action={
        <Button asChild variant="outline">
          <Link to={back}>Back</Link>
        </Button>
      }
    >
      Only admins create and change destinations.
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
          <ArrowLeft aria-hidden="true" /> Destinations
        </Link>
      </Button>
      <PageHeader eyebrow="Infrastructure" title="New destination" />
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
          <ArrowLeft aria-hidden="true" /> {d?.name ?? "Destination"}
        </Link>
      </Button>
      <PageHeader eyebrow="Edit destination" title={d?.name ?? "Destination"} />
      {role !== "ADMIN" ? (
        <AdminsOnly back={`/destinations/${id}`} />
      ) : loading && !d ? (
        <DetailSkeleton label="Loading destination" />
      ) : error && !d ? (
        <Alert variant="destructive">
          <AlertDescription>{userMessage(error)}</AlertDescription>
        </Alert>
      ) : d ? (
        <DestinationForm key={d.updatedAt} original={d} />
      ) : (
        <EmptyState icon={Lock} title="Destination not found">
          It was deleted or never existed.
        </EmptyState>
      )}
    </div>
  );
}
