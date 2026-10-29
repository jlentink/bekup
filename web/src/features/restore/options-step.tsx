import { FileKey2, Replace, Sparkles, Upload } from "lucide-react";
import { useId, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import type { RestoreMode } from "@/graphql/gen/graphql";
import { cn } from "@/lib/utils";

import { extractIdentity } from "@/lib/age-identity";

/** Largest identity file read (an age identity file is well under 1 KiB). */
const MAX_IDENTITY_FILE_BYTES = 16 * 1024;

export function OptionsStep({
  engine,
  mode,
  targetDatabase,
  targetName,
  keepOwners,
  onKeepOwners,
  needsIdentity,
  identity,
  onIdentity,
}: {
  engine: string;
  mode: RestoreMode;
  targetDatabase: string;
  targetName: string;
  keepOwners: boolean;
  onKeepOwners: (v: boolean) => void;
  needsIdentity: boolean;
  identity: string;
  onIdentity: (v: string) => void;
}) {
  const ownersId = useId();
  const overwrite = mode === "OVERWRITE";
  return (
    <div className="flex flex-col gap-8">
      <div
        className={cn(
          "flex items-start gap-3 rounded-md border p-4",
          overwrite ? "border-destructive/50 bg-status-failed-soft" : "bg-surface",
        )}
      >
        {overwrite ? (
          <Replace aria-hidden="true" className="mt-0.5 size-4 text-destructive" />
        ) : (
          <Sparkles aria-hidden="true" className="mt-0.5 size-4 text-primary" />
        )}
        <div className="flex flex-col gap-0.5">
          <span className="eyebrow">Mode {overwrite ? "OVERWRITE" : "NEW_DB"}</span>
          <span className="text-sm">
            {overwrite ? "Replace " : "Create "}
            <span className="font-mono font-medium">{targetDatabase}</span> on {targetName}
          </span>
        </div>
      </div>

      {engine === "POSTGRES" ? (
        <div className="flex items-start justify-between gap-6 border-b border-rule pb-6">
          <div className="flex flex-col gap-1">
            <label htmlFor={ownersId} className="font-medium">
              Keep object owners
            </label>
            <p className="max-w-prose text-sm text-muted-foreground">
              Off (recommended): every restored object belongs to the target connection's role. On:
              objects go to the roles of the same names on the target server, which may not be the
              roles you expect.
            </p>
          </div>
          <Switch id={ownersId} checked={keepOwners} onCheckedChange={onKeepOwners} />
        </div>
      ) : null}

      {needsIdentity ? (
        <IdentityField value={identity} onChange={onIdentity} />
      ) : (
        <p className="text-sm text-muted-foreground">
          bekup holds the key of this backup; no identity is needed.
        </p>
      )}
    </div>
  );
}

/**
 * The job's age identity for a recipient-encrypted backup. It stays in
 * component state only (never storage, never the cache), travels only in
 * the restore request and is cleared after it.
 */
function IdentityField({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const fieldId = useId();
  const hintId = useId();
  const fileRef = useRef<HTMLInputElement>(null);
  const [fileProblem, setFileProblem] = useState<string>();
  const valid = extractIdentity(value) !== null;

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <FileKey2 aria-hidden="true" className="size-4 text-primary" />
        <label htmlFor={fieldId} className="font-medium">
          Age identity
        </label>
      </div>
      <p id={hintId} className="max-w-prose text-sm text-muted-foreground">
        This backup is encrypted to an age recipient. Paste its private key (AGE-SECRET-KEY-1...) or
        load the identity file. It is sent only with the restore request, kept sealed by the server
        until the restore starts, and cleared from this page afterwards.
      </p>
      <textarea
        id={fieldId}
        value={value}
        rows={2}
        spellCheck={false}
        autoComplete="off"
        autoCorrect="off"
        autoCapitalize="off"
        data-1p-ignore
        data-lpignore="true"
        aria-describedby={hintId}
        aria-invalid={value !== "" && !valid}
        className={cn(
          "w-full max-w-2xl rounded-md border border-input bg-surface px-3 py-2 font-mono text-xs shadow-xs outline-none",
          "focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 dark:bg-surface-sunken",
          "aria-invalid:border-destructive",
        )}
        onChange={(e) => {
          onChange(e.target.value);
        }}
      />
      <div className="flex flex-wrap items-center gap-3">
        <input
          ref={fileRef}
          type="file"
          className="sr-only"
          tabIndex={-1}
          aria-hidden="true"
          accept=".txt,.key,.age,text/plain"
          onChange={(e) => {
            const file = e.target.files?.[0];
            e.target.value = "";
            setFileProblem(undefined);
            if (!file) return;
            if (file.size > MAX_IDENTITY_FILE_BYTES) {
              setFileProblem("That file is too large to be an age identity.");
              return;
            }
            void file.text().then((text) => {
              const key = extractIdentity(text);
              if (key) onChange(key);
              else setFileProblem("No AGE-SECRET-KEY-1 line found in that file.");
            });
          }}
        />
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => {
            fileRef.current?.click();
          }}
        >
          <Upload aria-hidden="true" />
          Load identity file
        </Button>
        {value !== "" ? (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => {
              onChange("");
            }}
          >
            Clear
          </Button>
        ) : null}
        <span role="status" className="text-sm">
          {fileProblem ? (
            <span className="text-destructive">{fileProblem}</span>
          ) : value === "" ? null : valid ? (
            <span className="text-status-success-fg">Identity recognized.</span>
          ) : (
            <span className="text-destructive">This is not an age identity.</span>
          )}
        </span>
      </div>
    </div>
  );
}
