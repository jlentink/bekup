import { History } from "lucide-react";

import { Tag } from "@/components/status/tag";
import type { TargetPrivilegeStatus } from "@/graphql/gen/graphql";
import { formatDateTime, formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

import { PRIVILEGE_META, privilegeReason } from "./labels";

/**
 * The stored result of the last connection or destination test (D-187):
 * pass/fail, the normalized failure code, when, and whether the settings
 * changed since. Never a message or raw error (the API stores none).
 */
export interface StoredTest {
  at: string;
  ok: boolean;
  code?: string | null;
  stale: boolean;
}

export interface StoredPrivileges {
  status: TargetPrivilegeStatus;
  reasons: readonly string[];
  acknowledgementRequired: boolean;
}

const STALE_HINT = "The settings changed after this test; run it again.";

/** List cell: Passed/Failed, the code, the time and a stale marker. */
export function LastTestCell({ test }: { test: StoredTest | null | undefined }) {
  if (!test) return <span className="text-xs text-muted-foreground">Not tested</span>;
  return (
    <div className="flex flex-col items-start gap-1" data-testid="last-test">
      <div className="flex flex-wrap items-center gap-1.5">
        <Tag tone={test.ok ? "success" : "danger"} title={formatDateTime(test.at)}>
          {test.ok ? "Passed" : "Failed"}
        </Tag>
        {test.stale ? (
          <Tag tone="neutral" title={STALE_HINT}>
            Stale
          </Tag>
        ) : null}
      </div>
      <span className="font-mono text-2xs text-muted-foreground">
        {!test.ok && test.code ? `${test.code} · ` : ""}
        <time dateTime={test.at}>{formatRelative(test.at)}</time>
      </span>
    </div>
  );
}

/** List cell: the privilege verdict of the stored test (D-168). */
export function PrivilegeVerdictCell({
  test,
}: {
  test: (StoredTest & { privileges?: StoredPrivileges | null }) | null | undefined;
}) {
  const status = test?.privileges?.status;
  if (!test) return <span className="text-xs text-muted-foreground">Not tested</span>;
  if (!status) return <span className="text-xs text-muted-foreground">Not checked</span>;
  const meta = PRIVILEGE_META[status];
  return (
    <div className="flex flex-col items-start gap-1">
      <Tag tone={meta.tone} title={meta.description}>
        {meta.label}
      </Tag>
      {test.stale ? (
        <span className="text-2xs text-muted-foreground">from a stale test</span>
      ) : null}
    </div>
  );
}

/**
 * Detail panel of the stored last test, shown until (and alongside) a test
 * run on this page. `privileges` only for connections.
 */
export function StoredTestPanel({
  test,
  privileges,
  emptyText,
}: {
  test: StoredTest | null | undefined;
  privileges?: StoredPrivileges | null | undefined;
  emptyText: string;
}) {
  if (!test) {
    return <p className="text-sm text-muted-foreground">{emptyText}</p>;
  }
  const meta = privileges ? PRIVILEGE_META[privileges.status] : undefined;
  return (
    <div
      data-testid="stored-test"
      className={cn(
        "flex flex-col gap-3 rounded-md border border-l-2 bg-surface px-4 py-3",
        test.ok ? "border-l-status-success" : "border-l-status-failed",
        test.stale && "border-dashed",
      )}
    >
      <div className="flex flex-wrap items-center gap-2">
        <History aria-hidden="true" className="size-3.5 text-muted-foreground" />
        <span className="eyebrow">Last test</span>
        <Tag tone={test.ok ? "success" : "danger"}>{test.ok ? "Passed" : "Failed"}</Tag>
        {!test.ok && test.code ? (
          <code className="font-mono text-2xs text-muted-foreground">{test.code}</code>
        ) : null}
        <time
          dateTime={test.at}
          title={formatDateTime(test.at)}
          className="ml-auto font-mono text-2xs text-muted-foreground"
        >
          {formatRelative(test.at)}
        </time>
      </div>
      {test.stale ? (
        <p className="text-xs text-status-partial-fg">
          Stale: the settings changed after this test. Run it again for a current result.
        </p>
      ) : null}
      {privileges && meta ? (
        <div className="flex flex-col gap-2 border-t border-rule pt-3">
          <div className="flex flex-wrap items-center gap-2">
            <span className="eyebrow">Role privileges</span>
            <Tag tone={meta.tone}>{meta.label}</Tag>
          </div>
          <p className="text-sm text-muted-foreground">{meta.description}</p>
          {privileges.reasons.length > 0 ? (
            <ul aria-label="Privilege reasons" className="flex flex-wrap gap-1.5">
              {privileges.reasons.map((r) => (
                <li
                  key={r}
                  title={r}
                  className="rounded-sm border bg-surface-sunken px-1.5 py-0.5 font-mono text-2xs"
                >
                  {privilegeReason(r)}
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
