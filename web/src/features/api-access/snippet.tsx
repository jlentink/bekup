import { Check, Copy } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { messages } from "@/lib/messages";

import { T } from "./messages";

/**
 * A ready-to-copy command or script: a heading, a short explanation and
 * the text in a scrollable mono block with a copy button. Only for public
 * text: snippets never contain a token.
 */
export function Snippet({ title, lead, code }: { title: string; lead: string; code: string }) {
  const [copied, setCopied] = useState(false);
  const headingId = useId();
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
    <div className="flex min-w-0 flex-col gap-1.5" role="group" aria-labelledby={headingId}>
      <div className="flex items-center justify-between gap-3">
        <h4 id={headingId} className="text-sm font-medium">
          {title}
        </h4>
        <Button
          type="button"
          variant="outline"
          size="xs"
          aria-label={T.examples.copy(title)}
          onClick={() => {
            navigator.clipboard.writeText(code).then(
              () => {
                setCopied(true);
                toast.success(T.examples.copied);
              },
              () => toast.error(T.examples.copyFailed),
            );
          }}
        >
          {copied ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
          {copied ? messages.common.copied : messages.common.copy}
        </Button>
      </div>
      <p className="text-xs text-muted-foreground">{lead}</p>
      <pre className="max-h-80 overflow-auto rounded-md border bg-surface-sunken px-3.5 py-2.5 font-mono text-xs leading-relaxed select-all">
        <code>{code}</code>
      </pre>
    </div>
  );
}
