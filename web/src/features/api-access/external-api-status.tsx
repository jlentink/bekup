import { Info } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { docs } from "@/features/admin/docs";
import { DocLink } from "@/features/admin/ui";

import { T } from "./messages";

/**
 * Shown instead of the curl examples when the external API does not run:
 * tokens can still be created in advance.
 */
export function ExternalApiDisabledNote() {
  return (
    <Alert data-testid="external-api-disabled">
      <Info aria-hidden="true" />
      <AlertTitle>{T.disabled.title}</AlertTitle>
      <AlertDescription>
        <p>
          {T.disabled.lead} <code className="font-mono text-xs">{T.disabled.command}</code>
          {T.disabled.after} <DocLink href={docs.externalApi}>{T.disabled.guide}</DocLink>
        </p>
      </AlertDescription>
    </Alert>
  );
}
