import { Copy, KeySquare } from "lucide-react";
import { useId, useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

import { CurlExamples } from "./curl-examples";
import { ExternalApiDisabledNote } from "./external-api-status";
import { T } from "./messages";
import { TOKEN_VARIABLE } from "./snippets";
import { useExternalApiEnabled } from "./use-external-api-enabled";

/** A token right after createApiToken: the plaintext exists only here. */
export interface IssuedToken {
  plaintext: string;
  name: string;
  /** The job the curl examples are filled with. */
  jobId: string;
}

/**
 * Shows a new API token once (D-220): a copy field, the instruction to
 * store it as the CI secret BEKUP_TOKEN, the curl examples (which use the
 * variable, never the token; a note instead while the external API is not
 * enabled) and the acknowledgement Done needs. The token
 * lives only in the caller's state and is dropped on Done.
 */
export function ShowOnceDialog({
  issued,
  onDone,
}: {
  issued: IssuedToken | null;
  onDone: () => void;
}) {
  const [saved, setSaved] = useState(false);
  const checkId = useId();
  const enabled = useExternalApiEnabled();
  const finish = () => {
    setSaved(false);
    onDone();
  };
  return (
    <Dialog
      open={issued !== null}
      onOpenChange={(o) => {
        if (!o && saved) finish();
      }}
    >
      <DialogContent
        className="max-h-[90vh] gap-5 overflow-y-auto sm:max-w-2xl"
        showCloseButton={false}
        onEscapeKeyDown={(e) => {
          if (!saved) e.preventDefault();
        }}
        onPointerDownOutside={(e) => {
          e.preventDefault();
        }}
      >
        <DialogHeader>
          <p className="eyebrow flex items-center gap-1.5">
            <KeySquare aria-hidden="true" className="size-3.5" /> {T.showOnce.eyebrow}
            {issued ? ` · ${issued.name}` : ""}
          </p>
          <DialogTitle className="font-display text-2xl font-normal">
            {T.showOnce.title}
          </DialogTitle>
          <DialogDescription>{T.showOnce.lead(TOKEN_VARIABLE)}</DialogDescription>
        </DialogHeader>
        <div className="flex items-start gap-2">
          <output
            aria-label={T.showOnce.tokenLabel}
            className="block min-w-0 flex-1 rounded-md border border-dashed border-border-strong/60 bg-surface-sunken px-4 py-3.5 font-mono text-sm break-all select-all"
          >
            {issued?.plaintext}
          </output>
          <Button
            variant="outline"
            size="sm"
            className="mt-2"
            onClick={() => {
              if (!issued) return;
              navigator.clipboard.writeText(issued.plaintext).then(
                () => toast.success(T.showOnce.copied),
                () => toast.error(T.showOnce.copyFailed),
              );
            }}
          >
            <Copy aria-hidden="true" /> {T.showOnce.copy}
          </Button>
        </div>
        {issued && enabled === false ? <ExternalApiDisabledNote /> : null}
        {issued && enabled ? <CurlExamples jobId={issued.jobId} /> : null}
        <DialogFooter className="items-center sm:justify-between">
          <label htmlFor={checkId} className="flex items-center gap-2 text-sm">
            <input
              id={checkId}
              type="checkbox"
              checked={saved}
              onChange={(e) => {
                setSaved(e.target.checked);
              }}
              className="size-4 accent-primary"
            />
            {T.showOnce.acknowledge}
          </label>
          <Button disabled={!saved} onClick={finish}>
            {T.showOnce.done}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
