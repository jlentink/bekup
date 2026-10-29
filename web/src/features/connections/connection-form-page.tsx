import { skipToken, useQuery } from "@apollo/client/react";
import { ArrowLeft, Lock } from "lucide-react";
import { Link, useParams } from "react-router";

import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { useSessionUser } from "@/features/auth/use-session-user";
import { userMessage } from "@/lib/api-error";

import { ConnectionForm } from "./connection-form";
import { ConnectionDetailQuery } from "./operations";
import { DetailSkeleton } from "./ui";

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
      Only admins create and change connections.
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
          <ArrowLeft aria-hidden="true" /> Connections
        </Link>
      </Button>
      <PageHeader
        eyebrow="Infrastructure"
        title="New connection"
        description="After creating it, confirm the server's certificate (or the bastion's host key) on the connection page."
      />
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
          <ArrowLeft aria-hidden="true" /> {c?.name ?? "Connection"}
        </Link>
      </Button>
      <PageHeader eyebrow="Edit connection" title={c?.name ?? "Connection"} />
      {role !== "ADMIN" ? (
        <AdminsOnly back={`/connections/${id}`} />
      ) : loading && !c ? (
        <DetailSkeleton label="Loading connection" />
      ) : error && !c ? (
        <Alert variant="destructive">
          <AlertDescription>{userMessage(error)}</AlertDescription>
        </Alert>
      ) : c ? (
        <ConnectionForm key={c.updatedAt} original={c} />
      ) : (
        <EmptyState icon={Lock} title="Connection not found">
          It was deleted or never existed.
        </EmptyState>
      )}
    </div>
  );
}
