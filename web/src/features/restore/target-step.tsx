import { useQuery } from "@apollo/client/react";
import {
  CircleAlert,
  Clock3,
  DatabaseZap,
  Plus,
  RefreshCw,
  Replace,
  ShieldAlert,
  Sparkles,
} from "lucide-react";
import { useId, useState } from "react";

import { Tag } from "@/components/status/tag";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import type {
  RestoreMode,
  RestoreTargetsQuery as RestoreTargetsResult,
  Role,
  RunItemKind,
} from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { formatDateTime, formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

import { ENGINE_LABELS, newDatabaseProblem, PRIVILEGE_LABELS, privilegeReasonLabel } from "./model";
import { RestoreTargetDatabasesQuery } from "./operations";
import { TemporaryConnectionPanel } from "./temp-connection";

export type RestoreTarget = RestoreTargetsResult["restoreTargets"]["targets"][number];

export interface TargetChoice {
  targetId: string | undefined;
  mode: RestoreMode;
  existingDatabase: string;
  newDatabase: string;
}

export function TargetStep({
  role,
  artifactId,
  kind,
  engine,
  targets,
  loading,
  error,
  choice,
  onChange,
  onTemporaryReady,
}: {
  role: Role;
  artifactId: string;
  kind: RunItemKind;
  engine: string;
  targets: readonly RestoreTarget[];
  loading: boolean;
  error: unknown;
  choice: TargetChoice;
  onChange: (next: Partial<TargetChoice>) => void;
  onTemporaryReady: (connectionId: string) => void;
}) {
  const [creating, setCreating] = useState(false);
  const selected = targets.find((t) => t.id === choice.targetId);

  if (loading && targets.length === 0) return <Skeleton className="h-48 w-full" />;
  if (error) {
    return (
      <Alert variant="destructive">
        <AlertDescription>{userMessage(error)}</AlertDescription>
      </Alert>
    );
  }

  return (
    <div className="flex flex-col gap-8">
      <fieldset className="flex flex-col gap-3">
        <legend className="mb-3 text-[0.9375rem] font-semibold tracking-tight">
          Target connection
        </legend>
        {targets.some((t) => t.existingRestricted) ? (
          <p className="text-sm text-muted-foreground">
            You can replace only the databases your own jobs back up, or create a new database on
            the connections they use.
          </p>
        ) : null}
        {targets.length === 0 ? (
          <p className="rounded-md border border-dashed px-4 py-6 text-center text-sm text-muted-foreground">
            No connection with the backup's engine is available to you as a restore target.
          </p>
        ) : (
          <div className="grid gap-2 md:grid-cols-2">
            {targets.map((t) => (
              <TargetCard
                key={t.id}
                target={t}
                checked={t.id === choice.targetId}
                onSelect={() => {
                  onChange({
                    targetId: t.id,
                    existingDatabase: "",
                    // A new database is the safer default when allowed.
                    mode: kind === "DATABASE" && !t.newDatabaseAllowed ? "OVERWRITE" : "NEW_DB",
                  });
                }}
              />
            ))}
          </div>
        )}
        {role === "ADMIN" ? (
          creating ? (
            <TemporaryConnectionPanel
              engine={engine}
              onClose={() => {
                setCreating(false);
              }}
              onReady={(id) => {
                setCreating(false);
                onTemporaryReady(id);
              }}
            />
          ) : (
            <div>
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  setCreating(true);
                }}
              >
                <Plus aria-hidden="true" />
                Create a temporary connection
              </Button>
            </div>
          )
        ) : null}
      </fieldset>

      {selected && kind === "DATABASE" ? (
        <DatabaseChoice
          artifactId={artifactId}
          target={selected}
          choice={choice}
          onChange={onChange}
        />
      ) : null}
    </div>
  );
}

function TargetCard({
  target: t,
  checked,
  onSelect,
}: {
  target: RestoreTarget;
  checked: boolean;
  onSelect: () => void;
}) {
  const descId = useId();
  return (
    <label
      className={cn(
        "group relative flex cursor-pointer flex-col gap-2 rounded-md border bg-surface p-4 transition-colors",
        "hover:border-border-strong has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-2 has-[:focus-visible]:outline-ring",
        checked && "border-primary bg-accent/60 shadow-raised",
        !t.ready && "cursor-not-allowed opacity-70",
      )}
    >
      <span className="flex items-center gap-3">
        <input
          type="radio"
          name="restore-target"
          className="size-4 accent-primary"
          checked={checked}
          disabled={!t.ready}
          aria-describedby={descId}
          onChange={onSelect}
        />
        <span className="truncate font-medium">{t.name}</span>
        <span className="eyebrow ml-auto shrink-0">
          {ENGINE_LABELS[t.engine] ?? t.engine} {t.versionKey}
        </span>
      </span>
      <span id={descId} className="flex flex-wrap gap-1.5 pl-7 text-xs">
        {t.original ? (
          <Tag compact tone="primary">
            Original source
          </Tag>
        ) : null}
        {t.temporary ? (
          <Tag compact tone="neutral">
            <Clock3 aria-hidden="true" className="size-3" />
            Temporary{t.expiresAt ? `, expires ${formatRelative(t.expiresAt)}` : ""}
          </Tag>
        ) : null}
        {t.insecureTransport ? (
          <Tag compact tone="insecure">
            Insecure transport
          </Tag>
        ) : null}
        {!t.ready ? (
          <Tag compact tone="danger" className="h-auto min-h-5 py-0.5 whitespace-normal">
            Not ready{t.notReadyReason ? `: ${t.notReadyReason}` : ""}
          </Tag>
        ) : null}
        {!t.newDatabaseAllowed ? (
          <Tag compact tone="neutral">
            No new databases
          </Tag>
        ) : null}
      </span>
    </label>
  );
}

function DatabaseChoice({
  artifactId,
  target,
  choice,
  onChange,
}: {
  artifactId: string;
  target: RestoreTarget;
  choice: TargetChoice;
  onChange: (next: Partial<TargetChoice>) => void;
}) {
  const nameId = useId();
  const problemId = useId();
  const [touched, setTouched] = useState(false);
  const problem = newDatabaseProblem(choice.newDatabase);

  return (
    <fieldset className="flex flex-col gap-4">
      <legend className="mb-3 text-[0.9375rem] font-semibold tracking-tight">
        Database on {target.name}
      </legend>
      <div role="radiogroup" aria-label="How to restore" className="grid gap-2 sm:grid-cols-2">
        <ModeOption
          checked={choice.mode === "NEW_DB"}
          disabled={!target.newDatabaseAllowed}
          icon={Sparkles}
          title="Create a new database"
          lead="Restores next to what exists. Nothing is replaced."
          onSelect={() => {
            onChange({ mode: "NEW_DB" });
          }}
        />
        <ModeOption
          checked={choice.mode === "OVERWRITE"}
          icon={Replace}
          title="Replace an existing database"
          lead="Overwrites the chosen database as a whole."
          destructive
          onSelect={() => {
            onChange({ mode: "OVERWRITE" });
          }}
        />
      </div>

      {choice.mode === "NEW_DB" ? (
        <div className="grid max-w-md gap-2">
          <Label htmlFor={nameId}>New database name</Label>
          <Input
            id={nameId}
            value={choice.newDatabase}
            autoComplete="off"
            spellCheck={false}
            className="font-mono"
            aria-invalid={touched && problem !== undefined}
            aria-describedby={touched && problem ? problemId : undefined}
            onBlur={() => {
              setTouched(true);
            }}
            onChange={(e) => {
              onChange({ newDatabase: e.target.value });
            }}
          />
          {touched && problem ? (
            <p id={problemId} className="text-sm text-destructive">
              {problem}
            </p>
          ) : null}
        </div>
      ) : (
        <ExistingDatabases
          key={target.id}
          artifactId={artifactId}
          targetId={target.id}
          value={choice.existingDatabase}
          onChange={(db) => {
            onChange({ existingDatabase: db });
          }}
        />
      )}
    </fieldset>
  );
}

function ModeOption({
  checked,
  disabled = false,
  icon: Icon,
  title,
  lead,
  destructive = false,
  onSelect,
}: {
  checked: boolean;
  disabled?: boolean;
  icon: typeof Sparkles;
  title: string;
  lead: string;
  destructive?: boolean;
  onSelect: () => void;
}) {
  return (
    <label
      className={cn(
        "flex cursor-pointer items-start gap-3 rounded-md border bg-surface p-3.5 transition-colors hover:border-border-strong",
        "has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-2 has-[:focus-visible]:outline-ring",
        checked && !destructive && "border-primary bg-accent/60",
        checked && destructive && "border-destructive/70 bg-status-failed-soft",
        disabled && "cursor-not-allowed opacity-60",
      )}
    >
      <input
        type="radio"
        name="restore-mode"
        className={cn("mt-0.5 size-4", destructive ? "accent-destructive" : "accent-primary")}
        checked={checked}
        disabled={disabled}
        onChange={onSelect}
      />
      <Icon
        aria-hidden="true"
        className={cn(
          "mt-0.5 size-3.5 shrink-0",
          destructive ? "text-destructive" : "text-primary",
        )}
      />
      <span className="flex flex-col gap-0.5 font-medium">
        {title}
        <span className="text-xs font-normal text-muted-foreground">
          {disabled ? "Not allowed on this connection." : lead}
        </span>
      </span>
    </label>
  );
}

function ExistingDatabases({
  artifactId,
  targetId,
  value,
  onChange,
}: {
  artifactId: string;
  targetId: string;
  value: string;
  onChange: (db: string) => void;
}) {
  const { data, loading, error, refetch } = useQuery(RestoreTargetDatabasesQuery, {
    variables: { artifactId, targetConnectionId: targetId },
    fetchPolicy: "network-only",
    notifyOnNetworkStatusChange: true,
  });
  const list = data?.restoreTargetDatabases;

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-3">
        <span className="eyebrow">Existing databases</span>
        {list ? (
          <span className="text-xs text-muted-foreground">
            listed {formatDateTime(list.listedAt)}
          </span>
        ) : null}
        <Button
          variant="ghost"
          size="xs"
          className="ml-auto"
          disabled={loading}
          onClick={() => {
            void refetch();
          }}
        >
          <RefreshCw aria-hidden="true" className={cn(loading && "animate-spin")} />
          Refresh
        </Button>
      </div>
      {loading && !list ? (
        <Skeleton className="h-24 w-full" />
      ) : error ? (
        <Alert variant="destructive">
          <AlertDescription>{userMessage(error)}</AlertDescription>
        </Alert>
      ) : list ? (
        <>
          {list.databases.length === 0 ? (
            <p className="rounded-md border border-dashed px-4 py-5 text-center text-sm text-muted-foreground">
              No databases you may replace on this connection.
            </p>
          ) : (
            <div
              role="radiogroup"
              aria-label="Database to replace"
              className="grid gap-1.5 sm:grid-cols-2 lg:grid-cols-3"
            >
              {list.databases.map((db) => (
                <label
                  key={db}
                  className={cn(
                    "flex cursor-pointer items-center gap-2.5 rounded-sm border bg-surface px-3 py-2 font-mono text-sm hover:border-border-strong",
                    "has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-ring",
                    db === value && "border-destructive/70 bg-status-failed-soft",
                  )}
                >
                  <input
                    type="radio"
                    name="restore-existing-db"
                    className="size-4 accent-destructive"
                    checked={db === value}
                    onChange={() => {
                      onChange(db);
                    }}
                  />
                  <DatabaseZap aria-hidden="true" className="size-3.5 text-muted-foreground" />
                  <span className="truncate">{db}</span>
                </label>
              ))}
            </div>
          )}
          {list.warnings.length > 0 ? (
            <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <CircleAlert aria-hidden="true" className="size-3.5" />
              Runner notes: <span className="font-mono">{list.warnings.join(", ")}</span>
            </p>
          ) : null}
          {list.targetPrivileges.acknowledgementRequired ? (
            <Alert variant="destructive">
              <ShieldAlert aria-hidden="true" />
              <AlertTitle>
                Privileged target role: {PRIVILEGE_LABELS[list.targetPrivileges.status]}
              </AlertTitle>
              <AlertDescription>
                {list.targetPrivileges.reasons.map(privilegeReasonLabel).join("; ")}. Only admins
                can restore into it, with an explicit acknowledgement in the preview.
              </AlertDescription>
            </Alert>
          ) : null}
        </>
      ) : null}
    </div>
  );
}
