import {
  ArrowRight,
  Cable,
  CircleCheck,
  DatabaseBackup,
  HardDrive,
  type LucideIcon,
} from "lucide-react";
import { Link } from "react-router";

import { EmptyState } from "@/components/empty-state";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

interface Step {
  icon: LucideIcon;
  title: string;
  body: string;
  done: boolean;
  to: string;
  action: string;
  /** The caller's role may do this step (connections and destinations are admin-managed). */
  allowed: boolean;
}

/**
 * Fresh-install guidance for staff: connection → destination → job, as a
 * three-entry ledger. Steps the role cannot do say who can.
 */
export function GettingStarted({
  admin,
  connections,
  destinations,
}: {
  admin: boolean;
  connections: number;
  destinations: number;
}) {
  const steps: Step[] = [
    {
      icon: Cable,
      title: "Create your first connection",
      body: "The database server to back up. bekup pins its TLS certificate before first use.",
      done: connections > 0,
      to: "/connections/new",
      action: "Add a connection",
      allowed: admin,
    },
    {
      icon: HardDrive,
      title: "Add a destination",
      body: "Where encrypted backups go: a disk path, an S3 bucket or an SFTP server.",
      done: destinations > 0,
      to: "/destinations/new",
      action: "Add a destination",
      allowed: admin,
    },
    {
      icon: DatabaseBackup,
      title: "Create a backup job",
      body: "Choose databases, a schedule and retention. Encryption is on by default.",
      done: false,
      to: "/jobs/new",
      action: "Create a job",
      allowed: true,
    },
  ];
  const current = steps.findIndex((s) => !s.done);
  return (
    <section
      aria-labelledby="getting-started"
      data-testid="getting-started"
      className="dash-tile dash-reveal rounded-lg border bg-card"
    >
      <header className="flex flex-col gap-1 border-b border-rule px-6 pt-5 pb-4">
        <p className="eyebrow">Getting started</p>
        <h2 id="getting-started" className="font-display text-3xl leading-none tracking-tight">
          Open the ledger
        </h2>
        <p className="max-w-prose text-sm text-muted-foreground">
          Nothing is being backed up yet. Three entries and the first backup runs on its own.
        </p>
      </header>
      <ol className="grid divide-y divide-rule md:grid-cols-3 md:divide-x md:divide-y-0">
        {steps.map((s, i) => {
          const Icon = s.done ? CircleCheck : s.icon;
          const active = i === current;
          return (
            <li
              key={s.title}
              data-testid="setup-step"
              data-done={s.done ? "true" : "false"}
              className={cn("flex flex-col gap-3 px-6 py-5", !active && !s.done && "opacity-80")}
            >
              <span className="flex items-center gap-3">
                <span className="font-display text-4xl leading-none text-muted-foreground">
                  {i + 1}
                </span>
                <Icon
                  aria-hidden="true"
                  className={cn("size-5", s.done ? "text-status-success" : "text-foreground")}
                />
              </span>
              <span className="flex flex-col gap-1">
                <span className="font-medium">
                  {s.title}
                  {s.done ? <span className="sr-only"> (done)</span> : null}
                </span>
                <span className="text-sm text-muted-foreground">{s.body}</span>
              </span>
              {s.done ? (
                <span className="text-xs font-medium text-status-success-fg">Done</span>
              ) : s.allowed ? (
                <Button
                  asChild
                  variant={active ? "default" : "outline"}
                  size="sm"
                  className="self-start"
                >
                  <Link to={s.to}>
                    {s.action}
                    <ArrowRight aria-hidden="true" />
                  </Link>
                </Button>
              ) : (
                <span className="text-xs text-muted-foreground">An administrator adds these.</span>
              )}
            </li>
          );
        })}
      </ol>
    </section>
  );
}

/** A backup owner without jobs. */
export function NoOwnedJobs() {
  return (
    <EmptyState icon={DatabaseBackup} title="No jobs assigned to you yet" className="dash-reveal">
      An administrator or maintainer makes you an owner of a backup job. Its health, runs and
      restores then appear here.
    </EmptyState>
  );
}
