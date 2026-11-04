import { useQuery } from "@apollo/client/react";
import { KeyRound, Plus } from "lucide-react";
import { useState } from "react";

import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { docs } from "@/features/admin/docs";
import { DocLink } from "@/features/admin/ui";
import { userMessage } from "@/lib/api-error";

import { AccessList } from "./access-list";
import { CreateDialog } from "./create-dialog";
import { ExternalApiDisabledNote } from "./external-api-status";
import { T } from "./messages";
import { ApiTokensQuery } from "./operations";
import { ShowOnceDialog, type IssuedToken } from "./show-once-dialog";
import { useExternalApiEnabled } from "./use-external-api-enabled";

/** /admin/api-access: every external API token (admins; D-220). */
export function ApiAccessPage() {
  const { data, loading, error } = useQuery(ApiTokensQuery, {
    variables: {},
    fetchPolicy: "cache-and-network",
  });
  const [creating, setCreating] = useState(false);
  const [issued, setIssued] = useState<IssuedToken | null>(null);
  const tokens = data?.apiTokens ?? [];
  const enabled = useExternalApiEnabled();
  const create = (
    <Button
      onClick={() => {
        setCreating(true);
      }}
    >
      <Plus aria-hidden="true" /> {T.panel.create}
    </Button>
  );
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        eyebrow={T.page.eyebrow}
        title={T.page.title}
        description={
          <>
            {T.page.lead} <DocLink href={docs.externalApi}>{T.page.guide}</DocLink>
          </>
        }
        actions={create}
      />
      {enabled === false ? <ExternalApiDisabledNote /> : null}
      {loading && !data ? (
        <Skeleton className="h-40 w-full" />
      ) : error && !data ? (
        <Alert variant="destructive">
          <AlertDescription>{userMessage(error)}</AlertDescription>
        </Alert>
      ) : tokens.length === 0 ? (
        <EmptyState icon={KeyRound} title={T.page.empty}>
          {T.page.emptyLead}
        </EmptyState>
      ) : (
        <AccessList tokens={tokens} />
      )}
      {creating ? <CreateDialog open onOpenChange={setCreating} onCreated={setIssued} /> : null}
      <ShowOnceDialog
        issued={issued}
        onDone={() => {
          setIssued(null);
        }}
      />
    </div>
  );
}
