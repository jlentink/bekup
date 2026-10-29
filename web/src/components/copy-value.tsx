import { Check, Copy } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { messages } from "@/lib/messages";
import { cn } from "@/lib/utils";

/**
 * A value an operator copies or compares (fingerprints, key lines, PEM):
 * mono, selectable, wrapped at any character, with a copy button. Only for
 * public data; never pass a secret.
 */
export function CopyValue({
  value,
  label,
  multiline = false,
  className,
}: {
  value: string;
  /** Accessible name of the copy button ("Copy fingerprint"). */
  label: string;
  multiline?: boolean;
  className?: string;
}) {
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const t = setTimeout(() => {
      setCopied(false);
    }, 1500);
    return () => {
      clearTimeout(t);
    };
  }, [copied]);
  return (
    <div
      className={cn(
        "group flex items-start gap-2 rounded-md border bg-surface-sunken py-1.5 pr-1.5 pl-3",
        className,
      )}
    >
      <code
        className={cn(
          "min-w-0 flex-1 py-0.5 font-mono text-xs leading-relaxed break-all select-all",
          multiline && "max-h-48 overflow-y-auto whitespace-pre-wrap",
        )}
      >
        {value}
      </code>
      <Button
        type="button"
        variant="ghost"
        size="icon-xs"
        aria-label={label}
        title={label}
        onClick={() => {
          navigator.clipboard.writeText(value).then(
            () => {
              setCopied(true);
            },
            () => toast.error(messages.errors.generic),
          );
        }}
      >
        {copied ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
      </Button>
    </div>
  );
}
