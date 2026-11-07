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
import { defineMessages } from "@/lib/i18n";
import { cn } from "@/lib/utils";

const T = defineMessages({
  en: {
    connectionTitle: "Create your first connection",
    connectionBody:
      "The database server to back up. bekup pins its TLS certificate before first use.",
    connectionAction: "Add a connection",
    destinationTitle: "Add a destination",
    destinationBody:
      "Where encrypted backups go: a disk path, an S3 bucket, an SFTP server or a WebDAV folder.",
    destinationAction: "Add a destination",
    jobTitle: "Create a backup job",
    jobBody: "Choose databases, a schedule and retention. Encryption is on by default.",
    jobAction: "Create a job",
    eyebrow: "Getting started",
    heading: "Open the ledger",
    lead: "Nothing is being backed up yet. Three entries and the first backup runs on its own.",
    doneSr: " (done)",
    done: "Done",
    adminOnly: "An administrator adds these.",
    noJobsTitle: "No jobs assigned to you yet",
    noJobsBody:
      "An administrator or maintainer makes you an owner of a backup job. Its health, runs and restores then appear here.",
  },
  nl: {
    connectionTitle: "Maak je eerste verbinding",
    connectionBody:
      "De databaseserver waarvan je een back-up maakt. bekup pint het TLS-certificaat vóór het eerste gebruik vast.",
    connectionAction: "Verbinding toevoegen",
    destinationTitle: "Voeg een bestemming toe",
    destinationBody:
      "Waar versleutelde back-ups heen gaan: een pad op schijf, een S3-bucket, een SFTP-server of een WebDAV-map.",
    destinationAction: "Bestemming toevoegen",
    jobTitle: "Maak een back-uptaak",
    jobBody: "Kies databases, een planning en bewaarbeleid. Versleuteling staat standaard aan.",
    jobAction: "Taak maken",
    eyebrow: "Aan de slag",
    heading: "Open het grootboek",
    lead: "Er wordt nog niets geback-upt. Drie stappen en de eerste back-up draait vanzelf.",
    doneSr: " (klaar)",
    done: "Klaar",
    adminOnly: "Een beheerder voegt deze toe.",
    noJobsTitle: "Er zijn nog geen taken aan je toegewezen",
    noJobsBody:
      "Een beheerder of onderhouder maakt je eigenaar van een back-uptaak. De gezondheid, runs en herstelacties verschijnen dan hier.",
  },
});

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
      title: T.connectionTitle,
      body: T.connectionBody,
      done: connections > 0,
      to: "/connections/new",
      action: T.connectionAction,
      allowed: admin,
    },
    {
      icon: HardDrive,
      title: T.destinationTitle,
      body: T.destinationBody,
      done: destinations > 0,
      to: "/destinations/new",
      action: T.destinationAction,
      allowed: admin,
    },
    {
      icon: DatabaseBackup,
      title: T.jobTitle,
      body: T.jobBody,
      done: false,
      to: "/jobs/new",
      action: T.jobAction,
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
        <p className="eyebrow">{T.eyebrow}</p>
        <h2 id="getting-started" className="font-display text-3xl leading-none tracking-tight">
          {T.heading}
        </h2>
        <p className="max-w-prose text-sm text-muted-foreground">{T.lead}</p>
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
                  {s.done ? <span className="sr-only">{T.doneSr}</span> : null}
                </span>
                <span className="text-sm text-muted-foreground">{s.body}</span>
              </span>
              {s.done ? (
                <span className="text-xs font-medium text-status-success-fg">{T.done}</span>
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
                <span className="text-xs text-muted-foreground">{T.adminOnly}</span>
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
    <EmptyState icon={DatabaseBackup} title={T.noJobsTitle} className="dash-reveal">
      {T.noJobsBody}
    </EmptyState>
  );
}
