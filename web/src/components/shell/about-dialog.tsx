import { useQuery } from "@apollo/client/react";
import { ExternalLink } from "lucide-react";

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { docs } from "@/features/admin/docs";
import { messages } from "@/lib/messages";
import { ServerInfoQuery } from "@/lib/server-info";

import type { Role } from "./nav";

const T = messages.about;

function ServerVersion() {
  // serverInfo is already loaded by the shell (insecure-transport banner);
  // admins only see it here, nothing new is shown to other roles.
  const { data, loading } = useQuery(ServerInfoQuery, { fetchPolicy: "cache-first" });
  const version = data?.serverInfo.version;
  return (
    <span className="font-mono text-xs" data-testid="about-server-version">
      {version ? T.version(version) : loading ? T.serverLoading : T.serverUnavailable}
    </span>
  );
}

function DocLink({ href, children }: { href: string; children: string }) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noreferrer"
      className="inline-flex items-center gap-1 text-sm font-medium text-primary underline-offset-4 hover:underline focus-visible:underline"
    >
      {children}
      <ExternalLink aria-hidden="true" className="size-3.5" />
      <span className="sr-only">{T.newTab}</span>
    </a>
  );
}

/**
 * About bekup: the web interface version (built in), for admins also the
 * server version, links to the documentation and release notes, and the
 * license. Opened from the account menu and the mobile navigation drawer.
 */
export function AboutDialog({
  open,
  onOpenChange,
  role,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  role: Role;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>{T.title}</DialogTitle>
          <DialogDescription>{T.description}</DialogDescription>
        </DialogHeader>
        <p className="font-mono text-sm font-semibold" data-testid="about-product">
          {T.product(__BEKUP_VERSION__)}
        </p>
        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
          <dt className="eyebrow self-center">{T.uiVersion}</dt>
          <dd className="font-mono text-xs" data-testid="about-ui-version">
            {T.version(__BEKUP_VERSION__)}
          </dd>
          {role === "ADMIN" ? (
            <>
              <dt className="eyebrow self-center">{T.serverVersion}</dt>
              <dd>
                <ServerVersion />
              </dd>
            </>
          ) : null}
          <dt className="eyebrow self-center">{T.license}</dt>
          <dd className="text-xs">{T.licenseName}</dd>
        </dl>
        <div className="flex flex-wrap gap-x-5 gap-y-2 border-t pt-4">
          <DocLink href={docs.home}>{T.docs}</DocLink>
          <DocLink href={docs.changelog}>{T.changelog}</DocLink>
        </div>
      </DialogContent>
    </Dialog>
  );
}
