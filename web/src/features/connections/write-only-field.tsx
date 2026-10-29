import { LockKeyhole, RotateCcw } from "lucide-react";
import type { ReactNode } from "react";

import { Tag } from "@/components/status/tag";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";

export type WriteOnlyAction = "keep" | "set" | "clear";

/**
 * The "set / change" UX of a write-only credential: a stored value is never
 * shown (only that one exists); the input appears only to replace it. When
 * the endpoint changed (D-140) the stored value cannot be kept and the
 * input is forced open with the reason.
 */
export function WriteOnlyField({
  label,
  stored,
  action,
  onAction,
  allowClear = false,
  reentry,
  children,
}: {
  label: string;
  stored: boolean;
  action: WriteOnlyAction;
  onAction: (a: WriteOnlyAction) => void;
  allowClear?: boolean;
  /** Why the stored value must be entered again (forces the input open). */
  reentry?: string | undefined;
  /** The input (a FormField) shown while setting a new value. */
  children: ReactNode;
}) {
  if (!stored) return <>{children}</>;
  if (reentry && action !== "clear") {
    return (
      <div className="flex flex-col gap-3">
        <Alert>
          <LockKeyhole aria-hidden="true" />
          <AlertDescription>{reentry}</AlertDescription>
        </Alert>
        {children}
        {allowClear ? (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="self-start"
            onClick={() => {
              onAction("clear");
            }}
          >
            Remove the stored {label.toLowerCase()} instead
          </Button>
        ) : null}
      </div>
    );
  }
  if (action === "set") {
    return (
      <div className="flex flex-col gap-2">
        {children}
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="self-start"
          onClick={() => {
            onAction("keep");
          }}
        >
          <RotateCcw aria-hidden="true" /> Keep the stored {label.toLowerCase()}
        </Button>
      </div>
    );
  }
  return (
    <div className="grid gap-2">
      <span className="text-sm font-medium">{label}</span>
      <div className="flex flex-wrap items-center gap-2">
        {action === "clear" ? (
          <Tag tone="warning">Removed on save</Tag>
        ) : (
          <Tag tone="success">
            <span className="sr-only">{label}: </span>Stored
          </Tag>
        )}
        <span className="text-xs text-muted-foreground">
          {action === "clear" ? "" : "Write-only: never shown again."}
        </span>
        {action === "clear" ? (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => {
              onAction("keep");
            }}
          >
            Undo
          </Button>
        ) : (
          <>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => {
                onAction("set");
              }}
            >
              Change {label.toLowerCase()}
            </Button>
            {allowClear ? (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => {
                  onAction("clear");
                }}
              >
                Remove
              </Button>
            ) : null}
          </>
        )}
      </div>
    </div>
  );
}
