import { ShieldAlert, ShieldCheck, ShieldOff } from "lucide-react";
import { Link } from "react-router";

import { cn } from "@/lib/utils";

import type { SecurityFinding } from "./model";
import { Tile, TileEmpty, TileError, TileSkeleton } from "./tile";

const ITEMS_MAX = 5;

/**
 * Weakened or failing security settings (D-001): unencrypted jobs,
 * insecure transport, privileged target roles, pin problems. The hazard
 * hatch marks every weakened setting; critical ones lead.
 */
export function SecurityTile({
  findings,
  loading,
  error,
  className,
}: {
  className?: string;
  findings: SecurityFinding[];
  loading: boolean;
  error: unknown;
}) {
  return (
    <Tile
      folio="05"
      title="Security"
      to="/connections"
      linkLabel="Connections"
      testId="tile-security"
      {...(className ? { className } : {})}
    >
      {loading && findings.length === 0 ? (
        <TileSkeleton rows={3} label="Loading security findings" />
      ) : error && findings.length === 0 ? (
        <TileError error={error} />
      ) : findings.length === 0 ? (
        <TileEmpty icon={<ShieldCheck aria-hidden="true" className="size-4 text-status-success" />}>
          No weakened settings: every job is encrypted and every connection is pinned.
        </TileEmpty>
      ) : (
        <ul aria-label="Security findings" className="grid gap-3 sm:grid-cols-2">
          {findings.map((f) => {
            const Icon = f.severity === "critical" ? ShieldAlert : ShieldOff;
            return (
              <li
                key={f.kind}
                data-testid="security-finding"
                data-kind={f.kind}
                data-severity={f.severity}
                className={cn(
                  "flex flex-col gap-2 rounded-md border px-3 py-2.5",
                  f.severity === "critical"
                    ? "border-status-failed/50 bg-status-failed-soft"
                    : "hatch-insecure border-status-insecure/60",
                )}
              >
                <span
                  className={cn(
                    "flex items-center gap-2 text-sm font-semibold",
                    f.severity === "critical" ? "text-status-failed-fg" : "text-status-insecure-fg",
                  )}
                >
                  <Icon aria-hidden="true" className="size-4 shrink-0" />
                  {f.title}
                  <span className="ml-auto font-mono text-xs" data-testid="finding-count">
                    {f.items.length}
                  </span>
                </span>
                <ul className="flex flex-col gap-0.5 text-xs">
                  {f.items.slice(0, ITEMS_MAX).map((item) => (
                    <li key={item.id} className="flex items-baseline gap-2">
                      <Link
                        to={item.to}
                        className="min-w-0 truncate font-medium text-foreground underline underline-offset-4"
                      >
                        {item.name}
                      </Link>
                      <span className="truncate text-muted-foreground">{item.detail}</span>
                    </li>
                  ))}
                  {f.items.length > ITEMS_MAX ? (
                    <li className="text-muted-foreground">and {f.items.length - ITEMS_MAX} more</li>
                  ) : null}
                </ul>
              </li>
            );
          })}
        </ul>
      )}
    </Tile>
  );
}
