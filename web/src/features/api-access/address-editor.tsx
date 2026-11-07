import { TriangleAlert } from "lucide-react";
import { useId } from "react";

import { Label } from "@/components/ui/label";
import { textareaClass } from "@/features/admin/ui";

import { MAX_ADDRESSES } from "./model";
import { T } from "./messages";
import { apiBaseUrl } from "./snippets";

/** Example networks, the same in every language. */
/** The API request shown as code (not translated). */
function selfRequest(base: string): string {
  return `GET ${base}/self`;
}

const ADDRESS_PLACEHOLDER = "203.0.113.0/24\n2001:db8::/48";

export interface AddressDraft {
  /** The editor's text: one network per line. */
  text: string;
  allowAny: boolean;
}

/**
 * The client address allowlist of a token (D-220 amendment 1): a list of
 * networks, or the explicit "Allow from any address" with its warning.
 * Exactly one of the two; the server re-checks everything.
 */
export function AddressEditor({
  value,
  onChange,
  error,
  disabled = false,
}: {
  value: AddressDraft;
  onChange: (v: AddressDraft) => void;
  error?: string | undefined;
  disabled?: boolean;
}) {
  const ids = { list: useId(), hint: useId(), error: useId(), any: useId(), warning: useId() };
  const describedBy = [ids.hint, error ? ids.error : null].filter(Boolean).join(" ");
  return (
    <fieldset className="flex flex-col gap-3" disabled={disabled}>
      <legend className="mb-1 text-sm font-medium">{T.addresses.legend}</legend>
      <div className="grid gap-2">
        <Label htmlFor={ids.list}>{T.addresses.label}</Label>
        <textarea
          id={ids.list}
          rows={4}
          spellCheck={false}
          autoComplete="off"
          className={textareaClass}
          value={value.text}
          disabled={disabled || value.allowAny}
          aria-invalid={error ? true : undefined}
          aria-describedby={describedBy}
          placeholder={ADDRESS_PLACEHOLDER}
          onChange={(e) => {
            onChange({ ...value, text: e.target.value });
          }}
        />
        {error ? (
          <p id={ids.error} className="text-xs text-destructive">
            {error}
          </p>
        ) : null}
        <div id={ids.hint} className="flex flex-col gap-1 text-xs text-muted-foreground">
          <p>{T.addresses.hint(MAX_ADDRESSES)}</p>
          <p>
            {T.addresses.selfHint}{" "}
            <code className="font-mono break-all">{selfRequest(apiBaseUrl())}</code>{" "}
            {T.addresses.selfHintAfter}
          </p>
          <p>{T.addresses.hostedRunners}</p>
        </div>
      </div>
      <label htmlFor={ids.any} className="flex items-start gap-2 text-sm">
        <input
          id={ids.any}
          type="checkbox"
          className="mt-0.5 size-4 accent-primary"
          checked={value.allowAny}
          aria-describedby={value.allowAny ? ids.warning : undefined}
          onChange={(e) => {
            onChange({ ...value, allowAny: e.target.checked });
          }}
        />
        {T.addresses.any}
      </label>
      {value.allowAny ? (
        <p
          id={ids.warning}
          className="flex items-start gap-2 rounded-md border border-status-partial/45 bg-status-partial-soft px-3 py-2 text-xs text-status-partial-fg"
        >
          <TriangleAlert aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" />
          {T.addresses.anyWarning}
        </p>
      ) : null}
    </fieldset>
  );
}
