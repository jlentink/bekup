import { useQuery } from "@apollo/client/react";
import { KeyRound, Plus } from "lucide-react";
import { useState } from "react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { userMessage } from "@/lib/api-error";

import { AccessList } from "./access-list";
import { CreateDialog } from "./create-dialog";
import { CurlExamples } from "./curl-examples";
import { ExternalApiDisabledNote } from "./external-api-status";
import { T } from "./messages";
import { ApiTokensQuery } from "./operations";
import { ShowOnceDialog, type IssuedToken } from "./show-once-dialog";
import { TOKEN_VARIABLE } from "./snippets";
import { useExternalApiEnabled } from "./use-external-api-enabled";

/**
 * The job's "API access" panel (D-220): its API tokens, creating one (with
 * the show-once dialog) and the curl examples for this job (a note instead
 * while the external API is not enabled on this server). Shown
 * to admins and the job's backup owners; the server re-checks everything.
 */
export function ApiAccessPanel({ jobId, jobName }: { jobId: string; jobName?: string }) {
  const { data, loading, error } = useQuery(ApiTokensQuery, {
    variables: { jobId },
    fetchPolicy: "cache-and-network",
  });
  const [creating, setCreating] = useState(false);
  const [issued, setIssued] = useState<IssuedToken | null>(null);
  const tokens = data?.apiTokens ?? [];
  const enabled = useExternalApiEnabled();
  return (
    <section
      aria-labelledby="job-api-access"
      data-testid="api-access-panel"
      className="flex flex-col gap-4 rounded-lg border bg-card px-5 py-4"
    >
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 id="job-api-access" className="flex items-center gap-2 text-[0.9375rem] font-semibold">
          <KeyRound aria-hidden="true" className="size-4 text-muted-foreground" /> {T.panel.title}
        </h2>
        <Button
          variant="outline"
          size="sm"
          onClick={() => {
            setCreating(true);
          }}
        >
          <Plus aria-hidden="true" /> {T.panel.create}
        </Button>
      </div>
      <p className="max-w-prose text-sm text-muted-foreground">{T.panel.lead}</p>
      {loading && !data ? (
        <Skeleton className="h-20 w-full" />
      ) : error && !data ? (
        <Alert variant="destructive">
          <AlertDescription>
            {T.panel.loadFailed} {userMessage(error)}
          </AlertDescription>
        </Alert>
      ) : tokens.length === 0 ? (
        <p className="text-sm text-muted-foreground">{T.panel.empty}</p>
      ) : (
        <AccessList tokens={tokens} jobId={jobId} />
      )}
      <div className="flex flex-col gap-3 border-t border-rule pt-4">
        <h3 className="text-sm font-semibold">{T.panel.examples}</h3>
        <p className="max-w-prose text-xs text-muted-foreground">
          {T.panel.examplesLead(TOKEN_VARIABLE)}
        </p>
        {enabled === false ? (
          <ExternalApiDisabledNote />
        ) : enabled ? (
          <CurlExamples jobId={jobId} />
        ) : null}
      </div>
      {creating ? (
        <CreateDialog
          open
          onOpenChange={setCreating}
          presetJobId={jobId}
          presetJobName={jobName}
          onCreated={setIssued}
        />
      ) : null}
      <ShowOnceDialog
        issued={issued}
        onDone={() => {
          setIssued(null);
        }}
      />
    </section>
  );
}
