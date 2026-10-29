import { PageHeader } from "@/components/page-header";
import { useRoleFlags } from "@/features/jobs/roles";

import { RunHistory } from "./run-history";

/** /runs: every run the caller may see, newest first, live. */
export function RunsListPage() {
  const flags = useRoleFlags();
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        eyebrow="Backups"
        title="Runs"
        description={
          flags.staff
            ? "Every backup run, newest first. Updates arrive live."
            : "The runs of the jobs you own, newest first."
        }
      />
      <RunHistory />
    </div>
  );
}
