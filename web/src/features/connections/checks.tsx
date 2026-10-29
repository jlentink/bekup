import { useMutation } from "@apollo/client/react";
import {
  CheckCircle2,
  Database,
  Download,
  Loader2,
  Radar,
  ScanSearch,
  ShieldAlert,
  XCircle,
} from "lucide-react";
import { useState, type ReactNode } from "react";

import { Tag } from "@/components/status/tag";
import { Button } from "@/components/ui/button";
import type {
  DetectConnectionVersionMutation,
  ListDatabasesMutation as ListDatabasesResult,
  PrePullConnectionImageMutation,
} from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { formatRelative } from "@/lib/format";
import { ListDatabasesMutation } from "@/lib/operations";
import { cn } from "@/lib/utils";

import { PRIVILEGE_META, privilegeReason, warningText } from "./labels";
import { DetectVersionMutation, PrePullImageMutation, TestConnectionMutation } from "./operations";
import { StoredTestPanel, type StoredPrivileges, type StoredTest } from "./last-test";
import type { ConnectionTestResult } from "./results";
import { Fact, Facts } from "./ui";

type Check = "test" | "detect" | "list" | "pull";

type Outcome =
  | { kind: "detect"; value: DetectConnectionVersionMutation["detectVersion"] }
  | { kind: "list"; value: ListDatabasesResult["listDatabases"] }
  | { kind: "pull"; value: PrePullConnectionImageMutation["prePullImage"] }
  | { kind: "error"; check: Check; message: string };

const CHECK_LABEL: Record<Check, string> = {
  test: "Test connection",
  detect: "Detect version",
  list: "List databases",
  pull: "Pre-pull image",
};

function Warnings({ codes }: { codes: readonly string[] }) {
  if (codes.length === 0) return null;
  return (
    <ul className="flex flex-col gap-1.5">
      {codes.map((w) => (
        <li key={w} className="flex items-start gap-2 text-sm text-status-partial-fg">
          <ShieldAlert aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" />
          <span>{warningText(w)}</span>
        </li>
      ))}
    </ul>
  );
}

function ResultFrame({
  ok,
  title,
  meta,
  children,
}: {
  ok: boolean;
  title: string;
  meta?: string;
  children?: ReactNode;
}) {
  const Icon = ok ? CheckCircle2 : XCircle;
  return (
    <div
      role="status"
      className={cn(
        "flex flex-col gap-4 rounded-md border-l-2 bg-surface-sunken/60 px-4 py-3.5",
        ok ? "border-l-status-success" : "border-l-status-failed",
      )}
    >
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <p className="flex items-center gap-2 font-medium">
          <Icon
            aria-hidden="true"
            className={cn("size-4", ok ? "text-status-success" : "text-status-failed")}
          />
          {title}
        </p>
        {meta ? <span className="font-mono text-2xs text-muted-foreground">{meta}</span> : null}
      </div>
      {children}
    </div>
  );
}

/** The testConnection result: version, compatibility, warnings and the privilege verdict. */
export function TestResultPanel({ result, at }: { result: ConnectionTestResult; at: number }) {
  const priv = result.privileges;
  const meta = priv ? PRIVILEGE_META[priv.status] : undefined;
  return (
    <ResultFrame
      ok={result.ok}
      title={result.ok ? "Connection works" : "Connection failed"}
      meta={`tested ${formatRelative(new Date(at).toISOString())}`}
    >
      {!result.ok && (result.message || result.code) ? (
        <p className="text-sm">
          {result.message}
          {result.code ? (
            <span className="ml-2 font-mono text-2xs text-muted-foreground">{result.code}</span>
          ) : null}
        </p>
      ) : null}
      <Facts>
        {result.serverVersion ? (
          <Fact term="Server" mono>
            {result.serverVersion}
          </Fact>
        ) : null}
        <Fact term="Detected key" mono>
          {result.detectedVersionKey ?? "no allowlisted client fits"}
        </Fact>
        <Fact term="Client image">
          {result.compatible ? (
            <Tag tone="success">Compatible</Tag>
          ) : (
            <Tag tone="danger">Not compatible</Tag>
          )}
          {result.legacy ? (
            <Tag tone="warning" className="ml-2">
              Legacy series
            </Tag>
          ) : null}
        </Fact>
        {result.ok ? <Fact term="Databases">{result.databaseCount}</Fact> : null}
      </Facts>
      <Warnings codes={result.warnings} />
      {priv && meta ? (
        <div className="flex flex-col gap-2 border-t border-rule pt-3">
          <div className="flex flex-wrap items-center gap-2">
            <span className="eyebrow">Role privileges</span>
            <Tag tone={meta.tone}>{meta.label}</Tag>
          </div>
          <p className="text-sm text-muted-foreground">{meta.description}</p>
          {priv.reasons.length > 0 ? (
            <ul aria-label="Privilege reasons" className="flex flex-wrap gap-1.5">
              {priv.reasons.map((r) => (
                <li
                  key={r}
                  title={r}
                  className="rounded-sm border bg-surface px-1.5 py-0.5 font-mono text-2xs"
                >
                  {privilegeReason(r)}
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : null}
    </ResultFrame>
  );
}

function OutcomePanel({ outcome }: { outcome: Outcome }) {
  switch (outcome.kind) {
    case "error":
      return (
        <ResultFrame ok={false} title={`${CHECK_LABEL[outcome.check]} failed`}>
          <p className="text-sm">{outcome.message}</p>
        </ResultFrame>
      );
    case "detect": {
      const v = outcome.value;
      return (
        <ResultFrame ok={v.currentCompatible} title={`Server version ${v.serverVersion}`}>
          <Facts>
            <Fact term="Suggested key" mono>
              {v.suggestedVersionKey ?? "none fits"}
            </Fact>
            <Fact term="Current image">
              {v.currentCompatible ? "fits the server" : "does not fit: edit the version key"}
            </Fact>
            {v.legacy ? <Fact term="Series">end of life (legacy)</Fact> : null}
          </Facts>
          <Warnings codes={v.warnings} />
        </ResultFrame>
      );
    }
    case "list":
      return (
        <ResultFrame
          ok
          title={`${outcome.value.databases.length} database${outcome.value.databases.length === 1 ? "" : "s"}`}
        >
          {outcome.value.databases.length > 0 ? (
            <ul className="grid gap-x-6 gap-y-1 font-mono text-xs sm:grid-cols-2 lg:grid-cols-3">
              {outcome.value.databases.map((d) => (
                <li key={d} className="flex items-center gap-2 break-all">
                  <Database aria-hidden="true" className="size-3 shrink-0 text-muted-foreground" />
                  {d}
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-sm text-muted-foreground">
              The server reports no user databases (system databases are hidden).
            </p>
          )}
          <Warnings codes={outcome.value.warnings} />
        </ResultFrame>
      );
    case "pull":
      return (
        <ResultFrame
          ok
          title={outcome.value.alreadyPresent ? "Image already present" : "Image pulled"}
        >
          <p className="font-mono text-xs break-all">{outcome.value.imageRef}</p>
        </ResultFrame>
      );
  }
}

/** A test result with the time it arrived (shown as "tested … ago"). */
function stamped(value: ConnectionTestResult): { value: ConnectionTestResult; at: number } {
  return { value, at: Date.now() };
}

/**
 * Runner checks on a connection. Each is its own request with one root
 * field (D-117) and rate limited by the server; target failures of a test
 * come back in its result, everything else as an error message.
 */
export function ConnectionChecks({
  id,
  lastTest,
}: {
  id: string;
  /** The stored last test (D-187), shown until a test runs on this page. */
  lastTest?: (StoredTest & { privileges?: StoredPrivileges | null }) | null;
}) {
  const [test] = useMutation(TestConnectionMutation);
  const [detect] = useMutation(DetectVersionMutation);
  const [list] = useMutation(ListDatabasesMutation);
  const [pull] = useMutation(PrePullImageMutation);
  const [busy, setBusy] = useState<Check>();
  const [outcome, setOutcome] = useState<Outcome>();
  const [showTest, setShowTest] = useState(true);
  // The full result of a test run on this page (messages, versions). The
  // stored outcome (lastTest) is refetched with the connection afterwards.
  const [last, setLast] = useState<{ value: ConnectionTestResult; at: number }>();

  const run = async (check: Check) => {
    setBusy(check);
    try {
      switch (check) {
        case "test": {
          const r = await test({
            variables: { id },
            // A separate request (D-117): the stored result and verdict.
            refetchQueries: ["ConnectionDetail"],
          });
          if (r.data) setLast(stamped(r.data.testConnection));
          setOutcome(undefined);
          setShowTest(true);
          break;
        }
        case "detect": {
          const r = await detect({ variables: { id } });
          if (r.data) setOutcome({ kind: "detect", value: r.data.detectVersion });
          setShowTest(false);
          break;
        }
        case "list": {
          const r = await list({ variables: { id } });
          if (r.data) setOutcome({ kind: "list", value: r.data.listDatabases });
          setShowTest(false);
          break;
        }
        case "pull": {
          const r = await pull({ variables: { id } });
          if (r.data) setOutcome({ kind: "pull", value: r.data.prePullImage });
          setShowTest(false);
          break;
        }
      }
    } catch (err) {
      setOutcome({ kind: "error", check, message: userMessage(err) });
      setShowTest(false);
    } finally {
      setBusy(undefined);
    }
  };

  const icons: Record<Check, typeof Radar> = {
    test: Radar,
    detect: ScanSearch,
    list: Database,
    pull: Download,
  };

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap gap-2">
        {(Object.keys(CHECK_LABEL) as Check[]).map((check) => {
          const Icon = icons[check];
          return (
            <Button
              key={check}
              type="button"
              variant={check === "test" ? "default" : "outline"}
              size="sm"
              disabled={busy !== undefined}
              onClick={() => void run(check)}
            >
              {busy === check ? (
                <Loader2 aria-hidden="true" className="animate-spin" />
              ) : (
                <Icon aria-hidden="true" />
              )}
              {CHECK_LABEL[check]}
            </Button>
          );
        })}
      </div>
      {outcome ? <OutcomePanel outcome={outcome} /> : null}
      {showTest && last ? <TestResultPanel result={last.value} at={last.at} /> : null}
      {!last ? (
        <StoredTestPanel
          test={lastTest}
          privileges={lastTest?.privileges}
          emptyText="Never tested. A test also reports whether the backup role is privileged (relevant for restores)."
        />
      ) : null}
    </div>
  );
}
