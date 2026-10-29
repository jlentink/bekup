import { LogoMark } from "@/components/brand/logo";
import { Skeleton } from "@/components/ui/skeleton";
import { messages } from "@/lib/messages";

/**
 * Placeholder of the app shell while the session loads: the same frame
 * (sidebar, top bar, content column) so nothing jumps when it arrives.
 */
export function ShellSkeleton() {
  return (
    <div className="flex min-h-dvh" aria-busy="true">
      <p role="status" className="sr-only">
        {messages.common.loading}
      </p>
      <div className="hidden w-(--shell-sidebar-width) shrink-0 flex-col gap-6 border-r border-sidebar-border bg-sidebar px-5 py-4 lg:flex">
        <LogoMark />
        {[3, 4, 2].map((n, i) => (
          <div key={i} className="flex flex-col gap-2">
            <Skeleton className="h-3 w-16" />
            {Array.from({ length: n }, (_, j) => (
              <Skeleton key={j} className="h-6 w-full" />
            ))}
          </div>
        ))}
      </div>
      <div className="flex flex-1 flex-col">
        <div className="flex h-(--shell-topbar-height) items-center justify-between border-b px-6">
          <Skeleton className="h-4 w-28" />
          <Skeleton className="size-7 rounded-sm" />
        </div>
        <div className="mx-auto flex w-full max-w-(--content-max-width) flex-col gap-6 px-4 py-8 sm:px-8">
          <Skeleton className="h-3 w-20" />
          <Skeleton className="h-9 w-64" />
          <Skeleton className="h-40 w-full" />
        </div>
      </div>
    </div>
  );
}

/** In-shell placeholder while a lazy page loads. */
export function PageSkeleton() {
  return (
    <div className="flex flex-col gap-6" aria-busy="true">
      <p role="status" className="sr-only">
        {messages.shell.loadingPage}
      </p>
      <div className="flex flex-col gap-2 border-b border-rule pb-6">
        <Skeleton className="h-3 w-20" />
        <Skeleton className="h-9 w-64" />
      </div>
      <Skeleton className="h-32 w-full" />
      <Skeleton className="h-48 w-full" />
    </div>
  );
}
