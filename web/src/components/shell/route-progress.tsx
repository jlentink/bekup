import { useNavigation } from "react-router";

import { cn } from "@/lib/utils";

/**
 * A hairline of ledger ink along the top edge while a route (a lazy page)
 * loads. Decorative: the page skeleton carries the accessible status.
 */
export function RouteProgress() {
  const navigation = useNavigation();
  const active = navigation.state !== "idle";
  return (
    <div
      aria-hidden="true"
      className={cn(
        "pointer-events-none fixed inset-x-0 top-0 z-50 h-0.5 origin-left bg-primary transition-[transform,opacity] duration-500 ease-out-soft",
        active ? "scale-x-75 opacity-100" : "scale-x-0 opacity-0",
      )}
    />
  );
}
