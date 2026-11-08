import {
  ArrowUpRight,
  FileCog,
  KeyRound,
  Mail,
  Network,
  ShieldAlert,
  ShieldCheck,
  type LucideIcon,
} from "lucide-react";
import { Link, NavLink, Outlet } from "react-router";

import { PageHeader } from "@/components/page-header";
import { defineMessages } from "@/lib/i18n";
import { cn } from "@/lib/utils";

const T = defineMessages({
  en: {
    eyebrow: "Administration",
    title: "Settings",
    lead: "Instance-wide settings. Changes that touch credentials or weaken a control need a fresh identity check and are written to the audit log.",
    nav: "Settings sections",
    related: "Related",
    twoFactor: "Two-factor policy",
    smtp: "E-mail (SMTP)",
    smtpHint: "Mail relay and transport security",
    alerts: "Security alerts",
    alertsHint: "security.* events in your inbox",
    network: "Network allowlists",
    networkHint: "Outbound guard and runner targets",
    configuration: "Configuration",
    configurationHint: "Reload bekup.toml",
    recovery: "Recovery",
    recoveryHint: "Passphrase and key escrow",
  },
  nl: {
    eyebrow: "Beheer",
    title: "Instellingen",
    lead: "Instellingen voor de hele instantie. Wijzigingen die inloggegevens raken of een beveiliging verzwakken, vragen opnieuw om je identiteit te bevestigen en komen in de auditlog.",
    nav: "Onderdelen van de instellingen",
    related: "Gerelateerd",
    twoFactor: "Beleid voor tweestapsverificatie",
    smtp: "E-mail (SMTP)",
    smtpHint: "Mailrelay en transportbeveiliging",
    alerts: "Beveiligingsmeldingen",
    alertsHint: "security.*-gebeurtenissen in je inbox",
    network: "Toegestane netwerklijsten",
    networkHint: "Uitgaande beveiliging en runnerdoelen",
    configuration: "Configuratie",
    configurationHint: "bekup.toml opnieuw laden",
    recovery: "Noodherstel",
    recoveryHint: "Wachtwoordzinnen en sleutelbewaring",
  },
});

const SECTIONS: readonly {
  to: string;
  label: string;
  hint: string;
  icon: LucideIcon;
  end: boolean;
}[] = [
  { to: "/admin/settings", label: T.smtp, hint: T.smtpHint, icon: Mail, end: true },
  {
    to: "/admin/settings/security-alerts",
    label: T.alerts,
    hint: T.alertsHint,
    icon: ShieldAlert,
    end: false,
  },
  {
    to: "/admin/settings/network",
    label: T.network,
    hint: T.networkHint,
    icon: Network,
    end: false,
  },
  {
    to: "/admin/settings/configuration",
    label: T.configuration,
    hint: T.configurationHint,
    icon: FileCog,
    end: false,
  },
  {
    to: "/admin/settings/recovery",
    label: T.recovery,
    hint: T.recoveryHint,
    icon: KeyRound,
    end: false,
  },
];

/** The admin settings frame: a section rail (links, one URL each) and the page. */
export function SettingsLayout() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader eyebrow={T.eyebrow} title={T.title} description={T.lead} />
      <div className="grid gap-8 lg:grid-cols-[15rem_minmax(0,1fr)]">
        <nav aria-label={T.nav} className="flex flex-col gap-4">
          <ol className="flex gap-1 overflow-x-auto border-b border-rule lg:flex-col lg:gap-0 lg:border-b-0 lg:border-l">
            {SECTIONS.map(({ to, label, hint, icon: Icon, end }, i) => (
              <li key={to} className="shrink-0">
                <NavLink
                  to={to}
                  end={end}
                  className={({ isActive }) =>
                    cn(
                      "group relative -mb-px flex items-start gap-3 border-b-2 border-transparent px-3 py-2.5 text-[0.8125rem] text-muted-foreground transition-colors hover:text-foreground lg:mb-0 lg:-ml-px lg:border-b-0 lg:border-l-2",
                      isActive && "border-primary text-foreground",
                    )
                  }
                >
                  <span
                    aria-hidden="true"
                    className="hidden pt-0.5 font-mono text-2xs text-muted-foreground tabular-nums lg:inline"
                  >
                    {String(i + 1).padStart(2, "0")}
                  </span>
                  <span className="flex flex-col gap-0.5">
                    <span className="inline-flex items-center gap-2 font-medium">
                      <Icon aria-hidden="true" className="size-3.5" />
                      {label}
                    </span>
                    <span className="hidden text-xs text-muted-foreground lg:block">{hint}</span>
                  </span>
                </NavLink>
              </li>
            ))}
          </ol>
          <div className="hidden flex-col gap-1.5 lg:flex">
            <p className="eyebrow px-3">{T.related}</p>
            <Link
              to="/admin/two-factor"
              className="inline-flex items-center gap-2 px-3 text-[0.8125rem] text-muted-foreground hover:text-foreground"
            >
              <ShieldCheck aria-hidden="true" className="size-3.5" />
              {T.twoFactor}
              <ArrowUpRight aria-hidden="true" className="size-3" />
            </Link>
          </div>
        </nav>
        <div className="max-w-5xl min-w-0">
          <Outlet />
        </div>
      </div>
    </div>
  );
}
