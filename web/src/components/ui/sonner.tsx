import {
  CircleCheckIcon,
  InfoIcon,
  Loader2Icon,
  OctagonXIcon,
  TriangleAlertIcon,
} from "lucide-react";
import { Toaster as Sonner, type ToasterProps } from "sonner";

import { useTheme } from "@/lib/theme";

/**
 * App-wide toaster. Its CSS comes from sonner/dist/styles.css (imported in
 * src/styles/csp-compat.css) instead of sonner's runtime <style> injection,
 * and its colors from the tokens via the [data-sonner-toaster] rules in
 * src/styles/index.css.
 */
function Toaster(props: ToasterProps) {
  const { resolved } = useTheme();
  return (
    <Sonner
      theme={resolved}
      className="toaster group"
      position="bottom-right"
      icons={{
        success: <CircleCheckIcon className="size-4 text-status-success" />,
        info: <InfoIcon className="size-4 text-status-running" />,
        warning: <TriangleAlertIcon className="size-4 text-status-partial" />,
        error: <OctagonXIcon className="size-4 text-status-failed" />,
        loading: <Loader2Icon className="size-4 animate-spin" />,
      }}
      {...props}
    />
  );
}

export { Toaster };
