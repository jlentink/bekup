import { PageHeader } from "@/components/page-header";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";

const TILES = ["Jobs healthy", "Failures (24h)", "Running now", "Next run"] as const;

/** Placeholder until task 6.2 builds the real dashboard. */
export function DashboardPage() {
  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        eyebrow="Overview"
        title="Dashboard"
        description="Job health, live runs, storage and security alerts. Built in task 6.2."
      />
      <section aria-label="Summary" className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {TILES.map((tile) => (
          <Card key={tile} className="gap-3">
            <CardHeader>
              <CardTitle className="eyebrow">{tile}</CardTitle>
            </CardHeader>
            <CardContent>
              <Skeleton className="h-8 w-20" />
            </CardContent>
          </Card>
        ))}
      </section>
      <Card>
        <CardHeader>
          <CardTitle>Recent runs</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {Array.from({ length: 5 }, (_, i) => (
            <Skeleton key={i} className="h-6 w-full" />
          ))}
        </CardContent>
      </Card>
    </div>
  );
}
