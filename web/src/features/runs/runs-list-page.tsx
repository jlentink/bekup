import { PageHeader } from "@/components/page-header";
import { useRoleFlags } from "@/features/jobs/roles";
import { defineMessages } from "@/lib/i18n";

import { RunHistory } from "./run-history";

const T = defineMessages({
  en: {
    eyebrow: "Backups",
    title: "Runs",
    staff: "Every backup run, newest first. Updates arrive live.",
    owner: "The runs of the jobs you own, newest first.",
  },
  nl: {
    eyebrow: "Back-ups",
    title: "Runs",
    staff: "Elke back-uprun, nieuwste eerst. Wijzigingen komen live binnen.",
    owner: "De runs van de taken waarvan je eigenaar bent, nieuwste eerst.",
  },
});

/** /runs: every run the caller may see, newest first, live. */
export function RunsListPage() {
  const flags = useRoleFlags();
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        eyebrow={T.eyebrow}
        title={T.title}
        description={flags.staff ? T.staff : T.owner}
      />
      <RunHistory />
    </div>
  );
}
