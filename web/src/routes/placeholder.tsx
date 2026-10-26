import { Construction } from "lucide-react";

import { PageHeader } from "@/components/page-header";

/** Stand-in for pages that later M6 tasks build. */
export function PlaceholderPage({ title, task }: { title: string; task: string }) {
  return (
    <div className="flex flex-col gap-8">
      <PageHeader title={title} />
      <div className="flex flex-col items-start gap-2 rounded-lg border border-dashed p-8">
        <Construction aria-hidden="true" className="size-5 text-muted-foreground" />
        <p className="font-medium">Not built yet</p>
        <p className="text-sm text-muted-foreground">
          This page arrives in task <span className="font-mono">{task}</span>.
        </p>
      </div>
    </div>
  );
}
