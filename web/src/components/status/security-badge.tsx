import {
  CircleCheck,
  Globe,
  Infinity as InfinityIcon,
  LockOpen,
  ShieldOff,
  Webhook,
  type LucideIcon,
} from "lucide-react";
import type { ComponentProps } from "react";

import { formatDateTime } from "@/lib/format";
import { defineMessages } from "@/lib/i18n";
import { messages } from "@/lib/messages";
import { cn } from "@/lib/utils";

import type { SecurityFlag } from "./status";

const L = defineMessages({
  en: {
    unencrypted: {
      label: "Unencrypted",
      description: "Backup encryption was turned off by an administrator.",
    },
    "insecure-transport": {
      label: "Insecure transport",
      description: "TLS verification was relaxed for this connection by an administrator.",
    },
    "personal-webhooks-widened": {
      label: "Personal webhooks widened",
      description:
        "An administrator let every user's personal webhooks reach a private network (SSRF allowlist).",
    },
    "any-address": {
      label: "Any address",
      description:
        "An administrator or a backup owner let this API token be used from any address: no client address allowlist protects it.",
    },
    "never-expires": {
      label: "Never expires",
      description:
        "An administrator or a backup owner created this API token without an expiry: it stays valid until it is revoked.",
    },
  },
  nl: {
    unencrypted: {
      label: "Niet versleuteld",
      description: "De versleuteling van back-ups is door een beheerder uitgeschakeld.",
    },
    "insecure-transport": {
      label: "Onveilig transport",
      description: "Een beheerder heeft de TLS-controle voor deze verbinding versoepeld.",
    },
    "personal-webhooks-widened": {
      label: "Persoonlijke webhooks verruimd",
      description:
        "Een beheerder heeft de persoonlijke webhooks van alle gebruikers toegang gegeven tot een privénetwerk (SSRF-toegestane lijst).",
    },
    "any-address": {
      label: "Elk adres",
      description:
        "Een beheerder of een eigenaar van een back-uptaak heeft dit API-token vanaf elk adres bruikbaar gemaakt: geen lijst met toegestane adressen beschermt het.",
    },
    "never-expires": {
      label: "Verloopt nooit",
      description:
        "Een beheerder of een eigenaar van een back-uptaak heeft dit API-token zonder vervaldatum aangemaakt: het blijft geldig totdat het wordt ingetrokken.",
    },
  },
});

const FLAG_ICON: Record<SecurityFlag, LucideIcon> = {
  unencrypted: LockOpen,
  "insecure-transport": ShieldOff,
  "personal-webhooks-widened": Webhook,
  "any-address": Globe,
  "never-expires": InfinityIcon,
};

const FLAG_META: Record<SecurityFlag, { label: string; description: string; icon: LucideIcon }> = {
  unencrypted: { ...L.unencrypted, icon: FLAG_ICON.unencrypted },
  "insecure-transport": { ...L["insecure-transport"], icon: FLAG_ICON["insecure-transport"] },
  "personal-webhooks-widened": {
    ...L["personal-webhooks-widened"],
    icon: FLAG_ICON["personal-webhooks-widened"],
  },
  "any-address": { ...L["any-address"], icon: FLAG_ICON["any-address"] },
  "never-expires": { ...L["never-expires"], icon: FLAG_ICON["never-expires"] },
};

interface SecurityBadgeProps extends Omit<ComponentProps<"span">, "children"> {
  flag: SecurityFlag;
  /**
   * The weakened setting is an accepted risk (D-228): the badge stays, muted
   * and signed off, with the end date ("" or null: no end date).
   */
  accepted?: { until: string | null } | undefined;
}

/** "Accepted until …" / "Accepted, no end date" (D-228). */
function acceptedText(until: string | null): string {
  if (!until) return messages.risks.acceptedForever;
  return messages.risks.acceptedUntil(formatDateTime(until));
}

/**
 * Permanent marker for a weakened security setting (D-001). The hazard hatch
 * is reserved for these badges and must not be used decoratively.
 */
export function SecurityBadge({ flag, accepted, className, ...props }: SecurityBadgeProps) {
  const meta = FLAG_META[flag];
  const Icon = meta.icon;
  if (accepted) {
    const text = acceptedText(accepted.until);
    return (
      <span
        data-slot="security-badge"
        data-flag={flag}
        data-accepted=""
        title={`${meta.description} ${text}.`}
        className={cn(
          "hatch-accepted inline-flex h-6 items-center gap-1.5 rounded-sm border border-dashed border-status-insecure/35 px-2",
          "font-mono text-2xs font-semibold tracking-[0.08em] text-muted-foreground uppercase",
          className,
        )}
        {...props}
      >
        <Icon aria-hidden="true" className="size-3.5 shrink-0 text-status-insecure-fg/70" />
        {meta.label}
        <CircleCheck aria-hidden="true" className="size-3.5 shrink-0" />
        <span className="font-sans text-2xs font-medium tracking-normal normal-case">{text}</span>
        <span className="sr-only">: {meta.description}</span>
      </span>
    );
  }
  return (
    <span
      data-slot="security-badge"
      data-flag={flag}
      title={meta.description}
      className={cn(
        "hatch-insecure inline-flex h-6 items-center gap-1.5 rounded-sm border border-status-insecure/60 px-2",
        "font-mono text-2xs font-semibold tracking-[0.08em] text-status-insecure-fg uppercase",
        className,
      )}
      {...props}
    >
      <Icon aria-hidden="true" className="size-3.5 shrink-0" />
      {meta.label}
      <span className="sr-only">: {meta.description}</span>
    </span>
  );
}
