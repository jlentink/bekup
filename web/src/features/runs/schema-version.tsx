import { GitCommitVertical } from "lucide-react";

import { Tag, type Tone } from "@/components/status/tag";
import type { SchemaVersionStatus } from "@/graphql/gen/graphql";
import { cn } from "@/lib/utils";

/** Strings of the schema-version display (D-210). */
const SCHEMA_VERSION_T = {
  label: "Schema version",
  status: {
    OK: "Captured",
    NONE: "No version",
    UNAVAILABLE: "Unavailable",
    INVALID: "Invalid result",
  } satisfies Record<SchemaVersionStatus, string>,
  hint: {
    OK: "The schema version read in the dump's snapshot.",
    NONE: "The version table does not exist in this database, has no row or holds an empty value.",
    UNAVAILABLE:
      "The version query failed (missing privilege, time limit, unexpected table layout or error). The backup is not affected.",
    INVALID:
      "The query returned more than 64 characters or characters other than printable ASCII; the value was discarded.",
  } satisfies Record<SchemaVersionStatus, string>,
} as const;

const TONE: Record<SchemaVersionStatus, Tone> = {
  OK: "success",
  NONE: "neutral",
  UNAVAILABLE: "warning",
  INVALID: "warning",
};

/**
 * One database's captured schema version: the value in monospace, or a
 * status tag with words (never colour only) when there is none.
 */
export function SchemaVersionValue({
  version,
  status,
  className,
  showLabel = true,
}: {
  version: string | null | undefined;
  status: SchemaVersionStatus | null | undefined;
  className?: string;
  showLabel?: boolean;
}) {
  if (!status) return null;
  return (
    <span
      className={cn("inline-flex min-w-0 items-center gap-1.5 text-xs", className)}
      data-testid="schema-version"
      title={SCHEMA_VERSION_T.hint[status]}
    >
      <GitCommitVertical aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
      {showLabel ? <span className="text-muted-foreground">{SCHEMA_VERSION_T.label}</span> : null}
      {status === "OK" && version ? (
        <span className="truncate font-mono text-foreground">{version}</span>
      ) : (
        <Tag tone={TONE[status]} compact>
          {SCHEMA_VERSION_T.status[status]}
        </Tag>
      )}
    </span>
  );
}
