import { ciScript, startSnippet, statusSnippet, apiBaseUrl } from "./snippets";
import { T } from "./messages";
import { Snippet } from "./snippet";

/**
 * The three CI snippets for a job (D-220, user requirement): start a
 * backup, check the status, the complete script. Filled with this
 * origin's /api/v1 and the job's id; the token is always $BEKUP_TOKEN.
 */
export function CurlExamples({ jobId }: { jobId: string }) {
  const base = apiBaseUrl();
  return (
    <section aria-label={T.examples.region} className="flex flex-col gap-4">
      <Snippet
        title={T.examples.start}
        lead={T.examples.startLead}
        code={startSnippet(base, jobId)}
      />
      <Snippet title={T.examples.status} lead={T.examples.statusLead} code={statusSnippet(base)} />
      <Snippet
        title={T.examples.script}
        lead={T.examples.scriptLead}
        code={ciScript(base, jobId)}
      />
    </section>
  );
}
