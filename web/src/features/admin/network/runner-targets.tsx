import { useQuery } from "@apollo/client/react";
import {
  Ban,
  CircleSlash,
  Database,
  FileLock2,
  Info,
  Route,
  ServerCog,
  TriangleAlert,
  Unplug,
} from "lucide-react";

import { Tag } from "@/components/status/tag";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Section } from "@/features/account/section";
import type { AdminRunnerTargetPolicyQuery } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";

import { docs } from "../docs";
import { RunnerTargetPolicyQuery } from "../operations";
import { DocLink } from "../ui";
import { RT } from "./runner-messages";

type Policy = AdminRunnerTargetPolicyQuery["runnerTargetPolicy"];

/** The runner's target allowlist, read-only (runner.toml [targets], D-223). */
export function RunnerTargetsPart() {
  const { data, error, loading, refetch } = useQuery(RunnerTargetPolicyQuery, {
    fetchPolicy: "cache-and-network",
  });
  const policy = data?.runnerTargetPolicy;
  const retry = () => {
    void refetch().catch(() => undefined);
  };

  return (
    <>
      <div className="flex flex-col gap-3 pt-6" data-testid="runner-targets-intro">
        <p className="flex items-start gap-2 rounded-md border border-rule bg-surface-sunken px-3.5 py-2.5 text-sm">
          <FileLock2 aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
          <span className="flex flex-col gap-1">
            <span data-testid="runner-targets-edit-note">{RT.editNote}</span>
            <DocLink href={docs.runnerTargets}>{RT.guide}</DocLink>
          </span>
        </p>
      </div>
      {error && !policy ? (
        <Unavailable reason={userMessage(error)} onRetry={retry} />
      ) : !policy ? (
        <div className="mt-6 flex flex-col gap-3" aria-busy="true" aria-label={RT.loading}>
          <Skeleton className="h-20 w-full" />
          <Skeleton className="h-20 w-full" />
        </div>
      ) : !policy.available ? (
        <Unavailable reason={policy.reason ?? ""} onRetry={retry} busy={loading} />
      ) : (
        <PolicyView policy={policy} />
      )}
    </>
  );
}

function Unavailable({
  reason,
  onRetry,
  busy = false,
}: {
  reason: string;
  onRetry: () => void;
  busy?: boolean;
}) {
  return (
    <Alert className="mt-6" data-testid="runner-targets-unavailable">
      <Unplug aria-hidden="true" />
      <AlertTitle>{RT.unavailableTitle}</AlertTitle>
      <AlertDescription className="flex flex-col gap-2">
        {reason ? <p>{reason}</p> : null}
        <p>{RT.unavailableLead}</p>
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="self-start"
          disabled={busy}
          onClick={onRetry}
        >
          {busy ? RT.loading : RT.retry}
        </Button>
      </AlertDescription>
    </Alert>
  );
}

function PolicyView({ policy }: { policy: Policy }) {
  const nothingAllowed = policy.allowCidrs.length === 0 && policy.allowHosts.length === 0;
  return (
    <div className="flex flex-col">
      {policy.warnings.length > 0 || policy.envOverrides.length > 0 || policy.truncated ? (
        <div className="flex flex-col gap-3 pt-6">
          {policy.warnings.length > 0 ? (
            <Alert data-testid="runner-targets-warnings">
              <TriangleAlert aria-hidden="true" />
              <AlertTitle>{RT.warningsTitle}</AlertTitle>
              <AlertDescription>
                <ul className="flex list-disc flex-col gap-1 pl-4">
                  {policy.warnings.map((w) => (
                    <li key={w}>{RT.warnings[w]}</li>
                  ))}
                </ul>
              </AlertDescription>
            </Alert>
          ) : null}
          {policy.envOverrides.length > 0 ? (
            <Alert data-testid="runner-targets-env">
              <Info aria-hidden="true" />
              <AlertTitle>{RT.envTitle}</AlertTitle>
              <AlertDescription>
                {RT.envLead(policy.envOverrides.map((k) => RT.keys[k]).join(", "))}
              </AlertDescription>
            </Alert>
          ) : null}
          {policy.truncated ? (
            <p className="text-sm text-status-partial-fg" data-testid="runner-targets-truncated">
              {RT.truncated}
            </p>
          ) : null}
        </div>
      ) : null}
      <Section
        icon={Database}
        title={RT.allowTitle}
        lead={RT.allowLead}
        status={
          nothingAllowed ? (
            <Tag tone="danger">{RT.nothingAllowedTag}</Tag>
          ) : (
            <Tag tone="neutral">{RT.readOnly}</Tag>
          )
        }
      >
        {nothingAllowed ? (
          <p
            className="text-sm font-medium text-status-failed-fg"
            data-testid="runner-nothing-allowed"
          >
            {RT.nothingAllowed}
          </p>
        ) : null}
        <ValueList
          label={RT.cidrs}
          values={policy.allowCidrs}
          empty={RT.noAllowCidrs}
          testId="runner-allow-cidrs"
        />
        <ValueList
          label={RT.hosts}
          values={policy.allowHosts}
          empty={RT.noAllowHosts}
          testId="runner-allow-hosts"
        />
      </Section>
      <Section icon={CircleSlash} title={RT.denyTitle} lead={RT.denyLead}>
        <ValueList
          label={RT.denyCidrs}
          values={policy.denyCidrs}
          empty={RT.noDenyCidrs}
          testId="runner-deny-cidrs"
        />
        <ValueList
          label={RT.denyHosts}
          values={policy.denyHosts}
          empty={RT.noDenyHosts}
          testId="runner-deny-hosts"
        />
      </Section>
      <Section icon={Route} title={RT.bastionsTitle} lead={RT.bastionsLead}>
        {policy.bastionTargets.length === 0 ? (
          <p className="text-sm text-status-partial-fg" data-testid="runner-no-bastions">
            {RT.noBastions}
          </p>
        ) : (
          <Table data-testid="runner-bastions">
            <caption className="sr-only">{RT.bastionsCaption}</caption>
            <TableHeader>
              <TableRow>
                <TableHead scope="col">{RT.bastion}</TableHead>
                <TableHead scope="col">{RT.bastionTargets}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {policy.bastionTargets.map((b) => (
                <TableRow key={b.bastion}>
                  <TableCell className="align-top font-mono text-xs">{b.bastion}</TableCell>
                  <TableCell>
                    <Chips values={b.targets} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Section>
      <Section icon={ServerCog} title={RT.ownTitle} lead={RT.ownLead}>
        <div className="flex flex-col gap-1.5" data-testid="runner-pool">
          <span className="eyebrow">{RT.pool}</span>
          {policy.runSubnetPool ? (
            <>
              <Chips values={[policy.runSubnetPool]} denied />
              <p className="text-xs text-muted-foreground">{RT.poolNote}</p>
            </>
          ) : (
            <p className="text-sm text-muted-foreground">{RT.noPool}</p>
          )}
        </div>
        <div className="flex flex-col gap-1.5" data-testid="runner-control">
          <span className="eyebrow">{RT.control}</span>
          {policy.controlNetworks.length > 0 ? (
            <Chips values={policy.controlNetworks} denied />
          ) : null}
          {policy.controlNetworksKnown ? null : (
            <p className="text-sm text-muted-foreground">{RT.controlPending}</p>
          )}
        </div>
      </Section>
      <Section icon={Ban} title={RT.deniedTitle} lead={RT.deniedLead}>
        <ul className="grid gap-2 text-sm" data-testid="runner-always-denied">
          {policy.alwaysDenied.map((d) => (
            <li key={d.category} className="flex flex-col gap-1">
              <span className="flex items-start gap-2">
                <Ban aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-status-failed" />
                {RT.categories[d.category]}
              </span>
              {d.cidrs.length > 0 ? (
                <span className="pl-5.5">
                  <Chips values={d.cidrs} />
                </span>
              ) : null}
            </li>
          ))}
        </ul>
      </Section>
    </div>
  );
}

function ValueList({
  label,
  values,
  empty,
  testId,
}: {
  label: string;
  values: readonly string[];
  empty: string;
  testId: string;
}) {
  return (
    <div className="flex flex-col gap-1.5" data-testid={testId}>
      <span className="eyebrow">{label}</span>
      {values.length === 0 ? (
        <p className="text-sm text-muted-foreground">{empty}</p>
      ) : (
        <Chips values={values} />
      )}
    </div>
  );
}

function Chips({ values, denied = false }: { values: readonly string[]; denied?: boolean }) {
  return (
    <ul className="flex flex-wrap gap-1.5">
      {values.map((v) => (
        <li
          key={v}
          className={
            denied
              ? "rounded-sm border border-status-failed/40 bg-status-failed-soft px-2 py-0.5 font-mono text-xs text-status-failed-fg"
              : "rounded-sm border bg-surface-sunken px-2 py-0.5 font-mono text-xs text-foreground"
          }
        >
          {v}
        </li>
      ))}
    </ul>
  );
}
