import { LockKeyhole, RotateCcw } from "lucide-react";
import type { ReactNode } from "react";

import { Tag } from "@/components/status/tag";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { defineMessages } from "@/lib/i18n";

const T = defineMessages({
  en: {
    removeInstead: (label: string) => `Remove the stored ${label.toLowerCase()} instead`,
    keep: (label: string) => `Keep the stored ${label.toLowerCase()}`,
    removedOnSave: "Removed on save",
    stored: "Stored",
    writeOnly: "Write-only: never shown again.",
    undo: "Undo",
    change: (label: string) => `Change ${label.toLowerCase()}`,
    remove: "Remove",
  },
  nl: {
    removeInstead: (label: string) =>
      `In plaats daarvan de opgeslagen waarde van „${label}” verwijderen`,
    keep: (label: string) => `De opgeslagen waarde van „${label}” behouden`,
    removedOnSave: "Wordt verwijderd bij opslaan",
    stored: "Opgeslagen",
    writeOnly: "Alleen-schrijven: wordt nooit meer getoond.",
    undo: "Ongedaan maken",
    change: (label: string) => `${label} wijzigen`,
    remove: "Verwijderen",
  },
});

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
            {T.removeInstead(label)}
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
          <RotateCcw aria-hidden="true" /> {T.keep(label)}
        </Button>
      </div>
    );
  }
  return (
    <div className="grid gap-2">
      <span className="text-sm font-medium">{label}</span>
      <div className="flex flex-wrap items-center gap-2">
        {action === "clear" ? (
          <Tag tone="warning">{T.removedOnSave}</Tag>
        ) : (
          <Tag tone="success">
            <span className="sr-only">{label}: </span>
            {T.stored}
          </Tag>
        )}
        <span className="text-xs text-muted-foreground">
          {action === "clear" ? "" : T.writeOnly}
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
            {T.undo}
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
              {T.change(label)}
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
                {T.remove}
              </Button>
            ) : null}
          </>
        )}
      </div>
    </div>
  );
}
