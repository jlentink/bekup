import {
  ArrowRight,
  CircleAlert,
  GitFork,
  Info,
  Lock,
  OctagonAlert,
  ShieldAlert,
  ShieldCheck,
} from "lucide-react";
import { useId } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
import type { RestorePreviewQuery as RestorePreviewResult } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { cn } from "@/lib/utils";

import { restoreMessages } from "./messages";
import {
  ENGINE_LABELS,
  PRIVILEGE_LABELS,
  privilegeReasonLabel,
  sortWarnings,
  warningSeverity,
  type PrivilegeGate,
  type WarningSeverity,
} from "./model";

const T = restoreMessages.preview;
const C = restoreMessages.common;

export type Preview = RestorePreviewResult["restorePreview"];

const SEVERITY_META: Record<
  WarningSeverity,
  { label: string; icon: typeof Info; row: string; icon_: string }
> = {
  destructive: {
    label: T.severity.destructive,
    icon: OctagonAlert,
    row: "border-l-destructive bg-status-failed-soft",
    icon_: "text-destructive",
  },
  caution: {
    label: T.severity.caution,
    icon: CircleAlert,
    row: "border-l-status-partial bg-status-partial-soft",
    icon_: "text-status-partial-fg",
  },
  info: {
    label: T.severity.info,
    icon: Info,
    row: "border-l-primary/60 bg-surface",
    icon_: "text-primary",
  },
};

export function PreviewStep({
  preview,
  loading,
  error,
  gate,
  acknowledged,
  onAcknowledge,
}: {
  preview: Preview | undefined;
  loading: boolean;
  error: unknown;
  gate: PrivilegeGate;
  acknowledged: boolean;
  onAcknowledge: (v: boolean) => void;
}) {
  if (error) {
    return (
      <Alert variant="destructive">
        <AlertTitle>{T.refused}</AlertTitle>
        <AlertDescription>{userMessage(error)}</AlertDescription>
      </Alert>
    );
  }
  if (loading || !preview) {
    return (
      <div className="flex flex-col gap-3" aria-busy="true">
        <Skeleton className="h-20 w-full" />
        <Skeleton className="h-14 w-full" />
        <Skeleton className="h-14 w-full" />
      </div>
    );
  }
  const warnings = sortWarnings(preview.warnings);
  const globals = preview.source.kind === "GLOBALS";

  return (
    <div className="flex flex-col gap-8">
      {/* What will happen: source → target, as the server understood it. */}
      <div className="grid items-stretch gap-2 sm:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)]">
        <div className="flex flex-col gap-1 rounded-md border bg-surface px-4 py-3">
          <span className="eyebrow">{T.fromBackup}</span>
          <span className="truncate font-mono text-base">
            {globals ? C.clusterGlobals : preview.source.database}
          </span>
          <span className="text-xs text-muted-foreground">
            {ENGINE_LABELS[preview.source.engine] ?? preview.source.engine}{" "}
            {preview.source.serverVersion}
          </span>
        </div>
        <ArrowRight
          aria-hidden="true"
          className="hidden size-5 self-center text-muted-foreground sm:block"
        />
        <div
          className={cn(
            "flex flex-col gap-1 rounded-md border px-4 py-3",
            preview.mode === "OVERWRITE" || globals
              ? "border-destructive/50 bg-status-failed-soft"
              : "bg-surface",
          )}
        >
          <span className="eyebrow">
            {globals ? T.intoServer : preview.mode === "OVERWRITE" ? C.replaces : C.creates}
          </span>
          <span className="truncate font-mono text-base">
            {globals ? preview.target.name : preview.targetDatabase}
          </span>
          <span className="text-xs text-muted-foreground">
            {globals ? T.everyDatabase : T.on(preview.target.name)}
            {preview.target.temporary ? T.temporaryConnection : ""}
          </span>
        </div>
      </div>

      {preview.crossSource ? (
        <div className="flex items-start gap-3 rounded-md border border-status-partial/50 bg-status-partial-soft px-4 py-3 text-sm">
          <GitFork aria-hidden="true" className="mt-0.5 size-4 text-status-partial-fg" />
          <p>
            <span className="font-semibold">{T.crossSource}</span>
            {T.crossSourceText}
          </p>
        </div>
      ) : null}

      <PrivilegePanel
        preview={preview}
        gate={gate}
        acknowledged={acknowledged}
        onAcknowledge={onAcknowledge}
      />

      <section aria-labelledby="restore-warnings" className="flex flex-col gap-3">
        <h3 id="restore-warnings" className="text-[0.9375rem] font-semibold tracking-tight">
          {T.beforeContinue}{" "}
          <span className="font-mono text-xs font-normal text-muted-foreground">
            ({warnings.length})
          </span>
        </h3>
        {warnings.length === 0 ? (
          <p className="text-sm text-muted-foreground">{T.noWarnings}</p>
        ) : (
          <ul className="flex flex-col gap-2">
            {warnings.map((w) => {
              const sev = warningSeverity(w.code);
              const meta = SEVERITY_META[sev];
              const Icon = w.code === "restore_hardening" ? Lock : meta.icon;
              return (
                <li
                  key={w.code}
                  data-severity={sev}
                  data-code={w.code}
                  className={cn(
                    "flex items-start gap-3 rounded-sm border border-l-4 px-4 py-3",
                    meta.row,
                  )}
                >
                  <Icon aria-hidden="true" className={cn("mt-0.5 size-4 shrink-0", meta.icon_)} />
                  <div className="flex min-w-0 flex-col gap-1">
                    <span className="flex flex-wrap items-baseline gap-2">
                      <span className={cn("eyebrow", sev === "destructive" && "text-destructive")}>
                        {meta.label}
                      </span>
                      <span className="font-mono text-2xs text-muted-foreground">{w.code}</span>
                    </span>
                    <p className="text-sm leading-relaxed">{w.message}</p>
                    {w.code === "restore_hardening" ? (
                      <p className="text-xs text-muted-foreground">
                        {T.grantBefore}
                        <code className="font-mono">{T.grantCode}</code>
                        {T.grantAfter}
                      </p>
                    ) : null}
                  </div>
                </li>
              );
            })}
          </ul>
        )}
      </section>
    </div>
  );
}

function PrivilegePanel({
  preview,
  gate,
  acknowledged,
  onAcknowledge,
}: {
  preview: Preview;
  gate: PrivilegeGate;
  acknowledged: boolean;
  onAcknowledge: (v: boolean) => void;
}) {
  const ackId = useId();
  const p = preview.targetPrivileges;
  if (gate === "clear") {
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <ShieldCheck aria-hidden="true" className="size-4 text-status-success" />
        {T.targetRole(PRIVILEGE_LABELS[p.status])}
      </div>
    );
  }
  return (
    <section
      aria-label={T.privilegedTarget}
      className="flex flex-col gap-3 rounded-md border-2 border-destructive/60 bg-status-failed-soft p-4"
    >
      <div className="flex items-start gap-3">
        <ShieldAlert aria-hidden="true" className="mt-0.5 size-5 shrink-0 text-destructive" />
        <div className="flex flex-col gap-1">
          <h3 className="font-semibold text-status-failed-fg">
            {T.privilegedRole(PRIVILEGE_LABELS[p.status])}
          </h3>
          <p className="text-sm">{T.privilegedRisk}</p>
          {p.reasons.length > 0 ? (
            <ul className="mt-1 flex flex-wrap gap-1.5">
              {p.reasons.map((r) => (
                <li
                  key={r}
                  className="rounded-xs border border-destructive/40 bg-surface px-1.5 py-0.5 text-xs"
                >
                  {privilegeReasonLabel(r)}
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      </div>
      {gate === "blocked" ? (
        <p role="alert" className="rounded-sm bg-surface px-3 py-2 text-sm font-medium">
          {T.adminOnly}
        </p>
      ) : (
        <label
          htmlFor={ackId}
          className="flex items-start gap-2.5 rounded-sm bg-surface px-3 py-2.5 text-sm"
        >
          <input
            id={ackId}
            type="checkbox"
            className="mt-0.5 size-4 accent-destructive"
            checked={acknowledged}
            onChange={(e) => {
              onAcknowledge(e.target.checked);
            }}
          />
          <span>
            {T.acknowledge}
            <span className="block text-xs text-muted-foreground">{T.acknowledgeNote}</span>
          </span>
        </label>
      )}
    </section>
  );
}
