import { useMutation, useQuery } from "@apollo/client/react";
import {
  Ban,
  Database,
  FileLock2,
  Globe,
  Info,
  Loader2,
  Network,
  Plus,
  RotateCcw,
  ShieldAlert,
  ShieldCheck,
  Trash2,
} from "lucide-react";
import { useId, useState, type ReactNode } from "react";
import { toast } from "sonner";

import { SecurityBadge } from "@/components/status/security-badge";
import { Tag } from "@/components/status/tag";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Section } from "@/features/account/section";
import { reportActionError, useStepUp } from "@/features/auth/use-step-up";
import { getFragmentData } from "@/graphql/gen";
import type { SsrfAllowlistFieldsFragment, SsrfScope } from "@/graphql/gen/graphql";
import { ApiCode, apiErrors, hasApiCode, userMessage } from "@/lib/api-error";
import { formatDateTime, formatRelative } from "@/lib/format";
import { defineMessages, plural } from "@/lib/i18n";
import { messages } from "@/lib/messages";
import { cn } from "@/lib/utils";

import {
  SsrfAllowlistFields,
  SsrfAllowlistQuery,
  UpdateSsrfAllowlistMutation,
} from "../operations";
import { docs } from "../docs";
import { DocLink } from "../ui";
import {
  ALL_SCOPES,
  changed,
  entries,
  fieldList,
  fieldRow,
  hint,
  newRow,
  scopeHint,
  toggleScope,
  widenings,
  type Entry,
  type ListKind,
  type Row,
} from "./model";
import { RT } from "./runner-messages";
import { RunnerTargetsPart } from "./runner-targets";

const T = defineMessages({
  en: {
    title: "Network allowlists",
    pageLead:
      "Two separate lists decide where connections may go: one for bekup's own connections to storage and notification services, and one for the runner's connections to database servers.",
    ownPart: "bekup's own connections (S3, SFTP, WebDAV, SMTP, webhooks)",
    ownIndex: "01",
    runnerIndex: "02",
    lead: "Every outbound connection of bekup (S3, SFTP, WebDAV, SMTP, webhooks) passes a dial-time SSRF guard. Public addresses are allowed; private ranges only when they are on this allowlist, and only for the uses each entry names.",
    runnerNote:
      "Database servers and SSH bastions are not reached through this list: the runner connects to them, under its own list shown below.",
    guide: "[netguard] reference",
    stepUp: "Changes need a step-up",
    cidrsTitle: "Networks (CIDRs)",
    cidrsLead:
      "Private ranges bekup may connect to, e.g. 10.20.0.0/16 or 192.0.2.10/32 for one address.",
    hostsTitle: "Host names",
    hostsLead:
      "Re-allow private addresses only for the exact name dialed, or *.suffix for its subdomains. IP literals go into the CIDR list.",
    config: "From bekup.toml",
    configLead: "Bootstrap entries of [netguard]; read-only here, always applied.",
    configScopes: "Applies to",
    configNeverPersonal: "Never to personal webhooks.",
    scopeNames: {
      DESTINATIONS: "Destinations",
      SMTP: "SMTP",
      SHARED_WEBHOOKS: "Shared webhooks",
      PERSONAL_WEBHOOKS: "Personal webhooks",
    } satisfies Record<SsrfScope, string>,
    scopeHelp: {
      DESTINATIONS: "S3, SFTP and WebDAV destinations",
      SMTP: "The SMTP server",
      SHARED_WEBHOOKS: "Notification webhooks configured by admins",
      PERSONAL_WEBHOOKS: "Webhooks any user can enter for themselves",
    } satisfies Record<SsrfScope, string>,
    scopesLegend: (kind: ListKind, n: number) =>
      `Uses of ${kind === "cidrs" ? "CIDR" : "host name"} ${String(n)}`,
    scopesTitle: "Scopes",
    scopesLead: (defaults: string) =>
      `Each entry opens a private target only for the uses ticked next to it. New entries start with: ${defaults}.`,
    personalBadgeLead:
      "At least one entry lets every user's personal webhooks reach a private target. This stays visible while any entry carries the personal webhooks scope.",
    noConfig: "No bootstrap entries.",
    managed: "Managed here",
    empty: "No entries.",
    add: (kind: ListKind) => (kind === "cidrs" ? "Add CIDR" : "Add host name"),
    entryLabel: (kind: ListKind, n: number) =>
      `${kind === "cidrs" ? "CIDR" : "Host name"} ${String(n)}`,
    remove: (v: string) => `Remove ${v || "empty entry"}`,
    placeholder: (kind: ListKind) => (kind === "cidrs" ? "10.20.0.0/16" : "minio.internal.example"),
    max: (used: number, max: number) => `${String(used)} of ${String(max)}`,
    tooMany: (max: number) => `At most ${String(max)} entries.`,
    ignoredTitle: "Some stored entries are not applied",
    ignored: (n: number) =>
      plural(n, {
        one: "1 stored entry is rejected by this server (for example loopback after leaving dev mode) and not applied. Saving the list drops it.",
        other: `${String(n)} stored entries are rejected by this server (for example loopback after leaving dev mode) and not applied. Saving the list drops them.`,
      }),
    loopback:
      "Dev mode: loopback (127.0.0.0/8, ::1) may be allowlisted. Outside dev mode it is always denied.",
    updated: (when: string) => `Last changed ${when}.`,
    never: "Never changed here.",
    widenTitle: "This widens what bekup may connect to",
    widenLead:
      "The new entries or uses let bekup reach more private hosts. The change is audited and raises a security alert to every admin.",
    widenNew: "new",
    personalTitle: "Every user's personal webhooks could reach these targets",
    personalLead:
      "Any user can enter a personal webhook URL. With the personal webhooks scope, every account (not only admins) can make bekup send requests to these private hosts or networks, which can be used to probe or hit internal services. Grant it only for a receiver that every user may reach. The change is audited and raises a security alert stating that personal webhooks gained access.",
    personalAck:
      "I understand that every user can make bekup send requests to these private targets",
    widenAck: "I understand that bekup may connect to these additional hosts or networks",
    save: "Save allowlist",
    saving: "Saving…",
    reset: "Discard changes",
    saved: "Allowlist saved; every bekup process applies it within seconds.",
    noChanges: "Nothing changed.",
    conflictTitle: "Changed in the meantime",
    conflict:
      "Another admin saved the allowlist after you opened this page. Reload it and apply your edits again.",
    reload: "Reload",
    rulesTitle: "What can never be allowed",
    rulesLead:
      "These ranges are denied before the allowlist is consulted, so no entry can open them.",
    softTitle: "Allowlistable private ranges",
    hard: [
      "Loopback (127.0.0.0/8, ::1), except in dev mode",
      "Link-local (169.254.0.0/16, fe80::/10)",
      "Cloud metadata (169.254.169.254, 100.100.100.200, fd00:ec2::254)",
      "Unspecified, multicast, broadcast and 240.0.0.0/4",
      "IPv4-compatible / translated IPv6, Teredo, IPv6 outside 2000::/3",
      "/0 or overly broad CIDRs that overlap any of the above",
    ],
    soft: [
      "RFC 1918: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16",
      "Carrier-grade NAT 100.64.0.0/10",
      "Unique local IPv6 fc00::/7",
      "Documentation and benchmarking ranges",
    ],
    noProxy:
      "Egress proxies are not supported: a proxy resolves and connects by itself and would bypass the dial-time check. Redirects are never followed.",
  },
  nl: {
    title: "Toegestane netwerklijsten",
    pageLead:
      "Twee aparte lijsten bepalen waarheen verbindingen mogen: één voor de eigen verbindingen van bekup met opslag- en meldingsdiensten, en één voor de verbindingen van de runner met databaseservers.",
    ownPart: "Eigen verbindingen van bekup (S3, SFTP, WebDAV, SMTP, webhooks)",
    ownIndex: "01",
    runnerIndex: "02",
    lead: "Elke uitgaande verbinding van bekup (S3, SFTP, WebDAV, SMTP, webhooks) gaat bij het verbinden door een SSRF-beveiliging. Publieke adressen zijn toegestaan; privébereiken alleen als ze op deze toegestane lijst staan, en alleen voor het gebruik dat elke vermelding noemt.",
    runnerNote:
      "Databaseservers en SSH-bastionhosts worden niet via deze lijst bereikt: de runner maakt verbinding met ze, volgens zijn eigen lijst hieronder.",
    guide: "Referentie [netguard]",
    stepUp: "Voor wijzigingen moet je je identiteit bevestigen",
    cidrsTitle: "Netwerken (CIDR's)",
    cidrsLead:
      "Privébereiken waarmee bekup verbinding mag maken, bijvoorbeeld 10.20.0.0/16, of 192.0.2.10/32 voor één adres.",
    hostsTitle: "Hostnamen",
    hostsLead:
      "Sta privéadressen opnieuw toe, alleen voor precies de gebelde naam, of *.achtervoegsel voor de subdomeinen ervan. IP-adressen horen in de CIDR-lijst.",
    config: "Uit bekup.toml",
    configLead: "Opstartvermeldingen van [netguard]; hier alleen-lezen, altijd toegepast.",
    configScopes: "Geldt voor",
    configNeverPersonal: "Nooit voor persoonlijke webhooks.",
    scopeNames: {
      DESTINATIONS: "Bestemmingen",
      SMTP: "SMTP",
      SHARED_WEBHOOKS: "Gedeelde webhooks",
      PERSONAL_WEBHOOKS: "Persoonlijke webhooks",
    },
    scopeHelp: {
      DESTINATIONS: "S3-, SFTP- en WebDAV-bestemmingen",
      SMTP: "De SMTP-server",
      SHARED_WEBHOOKS: "Meldingswebhooks die beheerders instellen",
      PERSONAL_WEBHOOKS: "Webhooks die elke gebruiker voor zichzelf kan invullen",
    },
    scopesLegend: (kind: ListKind, n: number) =>
      kind === "cidrs" ? `Gebruik van CIDR ${String(n)}` : `Gebruik van hostnaam ${String(n)}`,
    scopesTitle: "Gebruik",
    scopesLead: (defaults: string) =>
      `Elke vermelding opent een privédoel alleen voor het gebruik dat ernaast is aangevinkt. Nieuwe vermeldingen beginnen met: ${defaults}.`,
    personalBadgeLead:
      "Minstens één vermelding laat de persoonlijke webhooks van elke gebruiker een privédoel bereiken. Dit blijft zichtbaar zolang een vermelding het gebruik persoonlijke webhooks heeft.",
    noConfig: "Geen opstartvermeldingen.",
    managed: "Hier beheerd",
    empty: "Geen vermeldingen.",
    add: (kind: ListKind) => (kind === "cidrs" ? "CIDR toevoegen" : "Hostnaam toevoegen"),
    entryLabel: (kind: ListKind, n: number) =>
      kind === "cidrs" ? `CIDR ${String(n)}` : `Hostnaam ${String(n)}`,
    remove: (v: string) => (v ? `${v} verwijderen` : "Lege vermelding verwijderen"),
    placeholder: (kind: ListKind) => (kind === "cidrs" ? "10.20.0.0/16" : "minio.internal.example"),
    max: (used: number, max: number) => `${String(used)} van ${String(max)}`,
    tooMany: (max: number) => `Maximaal ${String(max)} vermeldingen.`,
    ignoredTitle: "Sommige opgeslagen vermeldingen worden niet toegepast",
    ignored: (n: number) =>
      plural(n, {
        one: "1 opgeslagen vermelding wordt door deze server geweigerd (bijvoorbeeld loopback na het verlaten van de dev-modus) en niet toegepast. Bij het opslaan van de lijst vervalt die.",
        other: `${String(n)} opgeslagen vermeldingen worden door deze server geweigerd (bijvoorbeeld loopback na het verlaten van de dev-modus) en niet toegepast. Bij het opslaan van de lijst vervallen die.`,
      }),
    loopback:
      "Dev-modus: loopback (127.0.0.0/8, ::1) mag op de toegestane lijst. Buiten de dev-modus wordt het altijd geweigerd.",
    updated: (when: string) => `Laatst gewijzigd ${when}.`,
    never: "Hier nooit gewijzigd.",
    widenTitle: "Dit verruimt waarmee bekup verbinding mag maken",
    widenLead:
      "Met de nieuwe vermeldingen of het nieuwe gebruik kan bekup meer privéhosts bereiken. De wijziging komt in de auditlog en stuurt elke beheerder een beveiligingsmelding.",
    widenNew: "nieuw",
    personalTitle: "De persoonlijke webhooks van elke gebruiker kunnen deze doelen bereiken",
    personalLead:
      "Elke gebruiker kan een persoonlijke webhook-URL invullen. Met het gebruik persoonlijke webhooks kan elk account (niet alleen beheerders) bekup verzoeken laten sturen naar deze privéhosts of -netwerken, wat kan worden misbruikt om interne diensten af te tasten of aan te vallen. Sta het alleen toe voor een ontvanger die elke gebruiker mag bereiken. De wijziging komt in de auditlog en stuurt een beveiligingsmelding dat persoonlijke webhooks toegang hebben gekregen.",
    personalAck:
      "Ik begrijp dat elke gebruiker bekup verzoeken kan laten sturen naar deze privédoelen",
    widenAck: "Ik begrijp dat bekup verbinding mag maken met deze extra hosts of netwerken",
    save: "Toegestane lijst opslaan",
    saving: "Opslaan…",
    reset: "Wijzigingen verwerpen",
    saved: "Toegestane lijst opgeslagen; elk bekup-proces past hem binnen enkele seconden toe.",
    noChanges: "Er is niets gewijzigd.",
    conflictTitle: "Intussen gewijzigd",
    conflict:
      "Een andere beheerder heeft de toegestane lijst opgeslagen nadat je deze pagina opende. Laad hem opnieuw en voer je wijzigingen nogmaals uit.",
    reload: "Opnieuw laden",
    rulesTitle: "Wat nooit kan worden toegestaan",
    rulesLead:
      "Deze bereiken worden geweigerd voordat de toegestane lijst wordt geraadpleegd, dus geen enkele vermelding kan ze openzetten.",
    softTitle: "Privébereiken die je kunt toestaan",
    hard: [
      "Loopback (127.0.0.0/8, ::1), behalve in de dev-modus",
      "Link-local (169.254.0.0/16, fe80::/10)",
      "Cloudmetadata (169.254.169.254, 100.100.100.200, fd00:ec2::254)",
      "Niet-gespecificeerd, multicast, broadcast en 240.0.0.0/4",
      "IPv4-compatibele / vertaalde IPv6, Teredo, IPv6 buiten 2000::/3",
      "/0 of te brede CIDR's die een van de bovenstaande overlappen",
    ],
    soft: [
      "RFC 1918: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16",
      "Carrier-grade NAT 100.64.0.0/10",
      "Unique local IPv6 fc00::/7",
      "Bereiken voor documentatie en benchmarks",
    ],
    noProxy:
      "Uitgaande proxy's worden niet ondersteund: een proxy zoekt namen op en verbindt zelf, en zou zo de controle bij het verbinden omzeilen. Doorverwijzingen worden nooit gevolgd.",
  },
});

type Errors = Partial<Record<string, string>>;

/**
 * The network allowlists: bekup's own SSRF allowlist (D-120, D-196:
 * bootstrap entries plus the admin list) and, read-only, the runner's
 * target allowlist (D-223).
 */
export function NetworkAllowlistPage() {
  const { data, error, refetch } = useQuery(SsrfAllowlistQuery, {
    fetchPolicy: "cache-and-network",
  });
  const list = data ? getFragmentData(SsrfAllowlistFields, data.ssrfAllowlist) : null;

  return (
    <div className="flex flex-col">
      <div className="flex max-w-prose flex-col gap-1 pb-2">
        <h2 className="font-display text-3xl">{T.title}</h2>
        <p className="text-sm text-muted-foreground">{T.pageLead}</p>
      </div>
      <Part
        index={T.ownIndex}
        title={T.ownPart}
        testId="network-part-own"
        aside={
          <>
            {list?.personalWebhooksWidened ? (
              <SecurityBadge
                flag="personal-webhooks-widened"
                data-testid="personal-widened-badge"
              />
            ) : null}
            <Tag tone="neutral">{T.stepUp}</Tag>
          </>
        }
        lead={
          <>
            <p>{T.lead}</p>
            <p className="flex items-start gap-2 text-foreground" data-testid="network-runner-note">
              <Database
                aria-hidden="true"
                className="mt-0.5 size-3.5 shrink-0 text-muted-foreground"
              />
              {T.runnerNote}
            </p>
            <DocLink href={docs.netguard}>{T.guide}</DocLink>
          </>
        }
      >
        {error && !data ? (
          <Alert variant="destructive" className="mt-6">
            <AlertDescription className="flex flex-wrap items-center gap-3">
              {userMessage(error)}
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  void refetch().catch(() => undefined);
                }}
              >
                {messages.common.retry}
              </Button>
            </AlertDescription>
          </Alert>
        ) : !list ? (
          <div className="mt-6 flex flex-col gap-3" aria-busy="true">
            <Skeleton className="h-24 w-full" />
            <Skeleton className="h-24 w-full" />
          </div>
        ) : (
          // A new server state (save, reload) starts a fresh draft.
          <AllowlistEditor
            key={list.updatedAt ?? "never"}
            list={list}
            onReload={async () => {
              await refetch();
            }}
          />
        )}
        <Section icon={Ban} title={T.rulesTitle} lead={T.rulesLead}>
          <ul className="grid gap-1.5 text-sm">
            {T.hard.map((r) => (
              <li key={r} className="flex items-start gap-2">
                <Ban aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-status-failed" />
                {r}
              </li>
            ))}
          </ul>
        </Section>
        <Section icon={Network} title={T.softTitle}>
          <ul className="grid gap-1.5 text-sm">
            {T.soft.map((r) => (
              <li key={r} className="flex items-start gap-2">
                <ShieldCheck
                  aria-hidden="true"
                  className="mt-0.5 size-3.5 shrink-0 text-muted-foreground"
                />
                {r}
              </li>
            ))}
          </ul>
          <p className="text-sm text-muted-foreground">{T.noProxy}</p>
        </Section>
      </Part>
      <Part
        index={T.runnerIndex}
        title={RT.title}
        testId="network-part-runner"
        aside={<Tag tone="neutral">{RT.readOnly}</Tag>}
        lead={<p>{RT.lead}</p>}
      >
        <RunnerTargetsPart />
      </Part>
    </div>
  );
}

/**
 * One of the page's two allowlists: a numbered band with its own heading,
 * so the two lists never read as one.
 */
function Part({
  index,
  title,
  lead,
  aside,
  testId,
  children,
}: {
  index: string;
  title: string;
  lead: ReactNode;
  aside: ReactNode;
  testId: string;
  children: ReactNode;
}) {
  const headingId = useId();
  return (
    <section aria-labelledby={headingId} data-testid={testId} className="flex flex-col pt-8">
      <div className="flex flex-wrap items-start justify-between gap-3 border-t-2 border-foreground/70 pt-4 pb-4">
        <div className="flex max-w-prose min-w-0 flex-col gap-1.5">
          <span
            aria-hidden="true"
            className="font-mono text-2xs text-muted-foreground tabular-nums"
          >
            {index}
          </span>
          <h2 id={headingId} className="font-display text-2xl">
            {title}
          </h2>
          <div className="flex flex-col gap-1.5 text-sm text-muted-foreground">{lead}</div>
        </div>
        <div className="flex flex-wrap items-center gap-2">{aside}</div>
      </div>
      <div className="flex flex-col border-t border-rule">{children}</div>
    </section>
  );
}

function toRows(list: readonly Entry[]): Row[] {
  return list.map((e) => newRow(e.value, e.scopes));
}

function scopeList(scopes: readonly SsrfScope[]): string {
  return scopes.map((s) => T.scopeNames[s]).join(", ");
}

function AllowlistEditor({
  list,
  onReload,
}: {
  list: SsrfAllowlistFieldsFragment;
  onReload: () => Promise<void>;
}) {
  const { withStepUp } = useStepUp();
  const [update] = useMutation(UpdateSsrfAllowlistMutation, {
    update(cache, { data }) {
      if (data) {
        cache.writeQuery({
          query: SsrfAllowlistQuery,
          data: { ssrfAllowlist: data.updateSsrfAllowlist },
        });
      }
    },
  });
  const [cidrs, setCidrs] = useState<Row[]>(() => toRows(list.cidrEntries));
  const [hosts, setHosts] = useState<Row[]>(() => toRows(list.hostEntries));
  const [errors, setErrors] = useState<Errors>({});
  const [ack, setAck] = useState(false);
  const [personalAck, setPersonalAck] = useState(false);
  const [conflict, setConflict] = useState(false);
  const [saving, setSaving] = useState(false);
  const ackId = useId();
  const personalAckId = useId();
  const warnId = useId();
  const personalWarnId = useId();

  const nextCidrs = entries(cidrs);
  const nextHosts = entries(hosts);
  const widened = [
    ...widenings("cidrs", list.cidrEntries, nextCidrs),
    ...widenings("hosts", list.hostEntries, nextHosts),
  ];
  const widening = widened.length > 0;
  const personal = widened.filter((w) => w.scopes.includes("PERSONAL_WEBHOOKS"));
  const dirty =
    changed("cidrs", list.cidrEntries, nextCidrs) ||
    changed("hosts", list.hostEntries, nextHosts) ||
    list.ignoredEntries > 0;
  const tooMany = nextCidrs.length > list.maxEntries || nextHosts.length > list.maxEntries;
  const missingScopes = [...nextCidrs, ...nextHosts].some((e) => e.scopes.length === 0);
  const newRowFor = () => newRow("", list.defaultScopes);

  const save = async () => {
    if (!dirty) {
      toast.info(T.noChanges);
      return;
    }
    // Server indexes refer to the submitted (non-empty) entries.
    const cidrKeys = cidrs.filter((r) => r.value.trim() !== "").map((r) => r.key);
    const hostKeys = hosts.filter((r) => r.value.trim() !== "").map((r) => r.key);
    setErrors({});
    setConflict(false);
    setSaving(true);
    try {
      await withStepUp(() =>
        update({
          variables: {
            input: {
              cidrEntries: nextCidrs.map((e) => ({ value: e.value, scopes: [...e.scopes] })),
              hostEntries: nextHosts.map((e) => ({ value: e.value, scopes: [...e.scopes] })),
              expectedUpdatedAt: list.updatedAt ?? null,
            },
          },
        }),
      );
      toast.success(T.saved);
    } catch (err) {
      if (hasApiCode(err, ApiCode.conflict)) {
        setConflict(true);
        return;
      }
      const next: Errors = {};
      for (const e of apiErrors(err)) {
        if (e.code !== ApiCode.validation) continue;
        const at = fieldRow(e.field);
        const key = at ? (at.kind === "cidrs" ? cidrKeys : hostKeys)[at.index] : undefined;
        const listKind = fieldList(e.field);
        if (at && key) next[at.part === "scopes" ? scopesKey(key) : key] = e.message;
        else if (listKind) next[listKind] = e.message;
      }
      if (Object.keys(next).length > 0) setErrors(next);
      else reportActionError(err);
    } finally {
      setSaving(false);
    }
  };

  return (
    <form
      noValidate
      aria-label={T.ownPart}
      onSubmit={(e) => {
        e.preventDefault();
        void save();
      }}
      className="flex flex-col"
    >
      <div className="flex flex-col gap-3 pt-6">
        {list.personalWebhooksWidened ? (
          <Alert data-testid="personal-widened-note">
            <ShieldAlert aria-hidden="true" />
            <AlertTitle>{T.scopeNames.PERSONAL_WEBHOOKS}</AlertTitle>
            <AlertDescription>{T.personalBadgeLead}</AlertDescription>
          </Alert>
        ) : null}
        {list.ignoredEntries > 0 ? (
          <Alert>
            <Info aria-hidden="true" />
            <AlertTitle>{T.ignoredTitle}</AlertTitle>
            <AlertDescription>{T.ignored(list.ignoredEntries)}</AlertDescription>
          </Alert>
        ) : null}
        {list.loopbackAllowed ? (
          <p className="text-sm text-muted-foreground" data-testid="allowlist-loopback-note">
            {T.loopback}
          </p>
        ) : null}
        <p className="text-sm text-muted-foreground" data-testid="allowlist-scopes-lead">
          {T.scopesLead(scopeList(list.defaultScopes))}
        </p>
        {conflict ? (
          <Alert variant="destructive">
            <AlertTitle>{T.conflictTitle}</AlertTitle>
            <AlertDescription className="flex flex-wrap items-center gap-3">
              {T.conflict}
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => {
                  setConflict(false);
                  void onReload().catch(() => undefined);
                }}
              >
                <RotateCcw aria-hidden="true" />
                {T.reload}
              </Button>
            </AlertDescription>
          </Alert>
        ) : null}
      </div>
      <Section icon={Network} title={T.cidrsTitle} lead={T.cidrsLead}>
        <ListEditor
          kind="cidrs"
          config={list.configCidrs}
          configScopes={list.configScopes}
          rows={cidrs}
          onChange={setCidrs}
          onAdd={newRowFor}
          errors={errors}
          max={list.maxEntries}
        />
      </Section>
      <Section icon={Globe} title={T.hostsTitle} lead={T.hostsLead}>
        <ListEditor
          kind="hosts"
          config={list.configHosts}
          configScopes={list.configScopes}
          rows={hosts}
          onChange={setHosts}
          onAdd={newRowFor}
          errors={errors}
          max={list.maxEntries}
        />
      </Section>
      <div className="flex flex-col gap-3 border-b border-rule py-6">
        {widening ? (
          <div
            role="alert"
            aria-labelledby={warnId}
            data-testid="allowlist-widening"
            className="hatch-insecure flex gap-3 rounded-md border border-status-insecure/50 px-4 py-3 text-status-insecure-fg"
          >
            <ShieldAlert aria-hidden="true" className="mt-0.5 size-5 shrink-0" />
            <div className="flex min-w-0 flex-col gap-2">
              <p id={warnId} className="font-semibold">
                {T.widenTitle}
              </p>
              <ul className="flex flex-col gap-1">
                {widened.map((w) => (
                  <li
                    key={`${w.kind}:${w.value}`}
                    className="flex flex-wrap items-center gap-1.5 text-xs"
                  >
                    <span className="rounded-sm border border-status-insecure/50 bg-surface px-1.5 font-mono">
                      {w.value}
                    </span>
                    {w.isNew ? <span className="eyebrow">{T.widenNew}</span> : null}
                    <span>{scopeList(w.scopes)}</span>
                  </li>
                ))}
              </ul>
              <p className="text-sm">{T.widenLead}</p>
              <label htmlFor={ackId} className="flex items-start gap-2.5 text-sm font-medium">
                <input
                  id={ackId}
                  type="checkbox"
                  className="mt-0.5 size-4 accent-primary"
                  checked={ack}
                  onChange={(e) => {
                    setAck(e.target.checked);
                  }}
                />
                {T.widenAck}
              </label>
            </div>
          </div>
        ) : null}
        {personal.length > 0 ? (
          <div
            role="alert"
            aria-labelledby={personalWarnId}
            data-testid="allowlist-personal-widening"
            className="flex gap-3 rounded-md border-2 border-destructive bg-status-failed-soft px-4 py-3 text-status-failed-fg"
          >
            <ShieldAlert aria-hidden="true" className="mt-0.5 size-5 shrink-0" />
            <div className="flex min-w-0 flex-col gap-2">
              <p id={personalWarnId} className="font-semibold">
                {T.personalTitle}
              </p>
              <ul className="flex flex-wrap gap-1.5">
                {personal.map((w) => (
                  <li
                    key={`${w.kind}:${w.value}`}
                    className="rounded-sm border border-destructive/50 bg-surface px-1.5 font-mono text-xs"
                  >
                    {w.value}
                  </li>
                ))}
              </ul>
              <p className="text-sm">{T.personalLead}</p>
              <label
                htmlFor={personalAckId}
                className="flex items-start gap-2.5 text-sm font-medium"
              >
                <input
                  id={personalAckId}
                  type="checkbox"
                  className="mt-0.5 size-4 accent-destructive"
                  checked={personalAck}
                  onChange={(e) => {
                    setPersonalAck(e.target.checked);
                  }}
                />
                {T.personalAck}
              </label>
            </div>
          </div>
        ) : null}
        <div className="flex flex-wrap items-center gap-3">
          <Button
            type="submit"
            variant={widening ? "destructive" : "default"}
            disabled={
              saving ||
              !dirty ||
              tooMany ||
              missingScopes ||
              (widening && !ack) ||
              (personal.length > 0 && !personalAck)
            }
          >
            {saving ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
            {saving ? T.saving : T.save}
          </Button>
          {dirty && list.ignoredEntries === 0 ? (
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                setCidrs(toRows(list.cidrEntries));
                setHosts(toRows(list.hostEntries));
                setErrors({});
                setAck(false);
                setPersonalAck(false);
              }}
            >
              {T.reset}
            </Button>
          ) : null}
          <span className="text-xs text-muted-foreground">
            {list.updatedAt ? (
              <time dateTime={list.updatedAt} title={formatDateTime(list.updatedAt)}>
                {T.updated(formatRelative(list.updatedAt))}
              </time>
            ) : (
              T.never
            )}
          </span>
        </div>
      </div>
    </form>
  );
}

/** The error key of a row's scopes. */
function scopesKey(rowKey: string): string {
  return `${rowKey}:scopes`;
}

function ListEditor({
  kind,
  config,
  configScopes,
  rows,
  onChange,
  onAdd,
  errors,
  max,
}: {
  kind: ListKind;
  config: readonly string[];
  configScopes: readonly SsrfScope[];
  rows: Row[];
  onChange: (rows: Row[]) => void;
  onAdd: () => Row;
  errors: Errors;
  max: number;
}) {
  const used = rows.filter((r) => r.value.trim() !== "").length;
  const listError = errors[kind];
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-col gap-1.5" data-testid={`allowlist-config-${kind}`}>
        <div className="flex items-center gap-2">
          <FileLock2 aria-hidden="true" className="size-3.5 text-muted-foreground" />
          <span className="eyebrow">{T.config}</span>
        </div>
        <p className="text-xs text-muted-foreground">{T.configLead}</p>
        {config.length === 0 ? (
          <p className="text-sm text-muted-foreground">{T.noConfig}</p>
        ) : (
          <>
            <ul className="flex flex-wrap gap-1.5">
              {config.map((v) => (
                <li
                  key={v}
                  className="rounded-sm border bg-surface-sunken px-2 py-0.5 font-mono text-xs text-muted-foreground"
                >
                  {v}
                </li>
              ))}
            </ul>
            <p
              className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground"
              data-testid={`allowlist-config-scopes-${kind}`}
            >
              <span className="eyebrow">{T.configScopes}</span>
              {configScopes.map((s) => (
                <Tag key={s} compact tone="neutral">
                  {T.scopeNames[s]}
                </Tag>
              ))}
              {configScopes.includes("PERSONAL_WEBHOOKS") ? null : (
                <span>{T.configNeverPersonal}</span>
              )}
            </p>
          </>
        )}
      </div>
      <div className="flex flex-col gap-2">
        <div className="flex items-center justify-between gap-2">
          <span className="eyebrow">{T.managed}</span>
          <span
            className={cn(
              "font-mono text-2xs",
              used > max ? "text-destructive" : "text-muted-foreground",
            )}
          >
            {T.max(used, max)}
          </span>
        </div>
        {rows.length === 0 ? <p className="text-sm text-muted-foreground">{T.empty}</p> : null}
        <ul className="flex flex-col gap-3">
          {rows.map((r, i) => (
            <EntryRow
              key={r.key}
              kind={kind}
              index={i}
              row={r}
              error={errors[r.key]}
              scopesError={errors[scopesKey(r.key)]}
              onChange={(next) => {
                onChange(rows.map((x) => (x.key === r.key ? { ...x, ...next } : x)));
              }}
              onRemove={() => {
                onChange(rows.filter((x) => x.key !== r.key));
              }}
            />
          ))}
        </ul>
        {used > max ? <p className="text-sm text-destructive">{T.tooMany(max)}</p> : null}
        {listError ? <p className="text-sm text-destructive">{listError}</p> : null}
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="self-start"
          disabled={rows.length >= max}
          onClick={() => {
            onChange([...rows, onAdd()]);
          }}
        >
          <Plus aria-hidden="true" />
          {T.add(kind)}
        </Button>
      </div>
    </div>
  );
}

function EntryRow({
  kind,
  index,
  row,
  error,
  scopesError,
  onChange,
  onRemove,
}: {
  kind: ListKind;
  index: number;
  row: Row;
  error: string | undefined;
  scopesError: string | undefined;
  onChange: (next: Partial<Pick<Row, "value" | "scopes">>) => void;
  onRemove: () => void;
}) {
  const id = useId();
  const tip = error ?? hint(kind, row.value);
  const scopeTip = scopesError ?? (row.value.trim() === "" ? null : scopeHint(row.scopes));
  const personalOn = row.scopes.includes("PERSONAL_WEBHOOKS");
  return (
    <li
      className={cn(
        "flex flex-col gap-1.5 rounded-md border px-3 py-2.5",
        personalOn ? "border-destructive/60" : "border-rule",
      )}
      data-testid="allowlist-entry"
    >
      <div className="flex items-center gap-2">
        <label htmlFor={id} className="sr-only">
          {T.entryLabel(kind, index + 1)}
        </label>
        <Input
          id={id}
          value={row.value}
          spellCheck={false}
          autoCapitalize="off"
          autoComplete="off"
          maxLength={260}
          placeholder={T.placeholder(kind)}
          aria-invalid={error ? true : undefined}
          aria-describedby={tip ? `${id}-tip` : undefined}
          className="font-mono text-sm"
          onChange={(e) => {
            onChange({ value: e.target.value });
          }}
        />
        <Button
          type="button"
          variant="ghost"
          size="icon"
          aria-label={T.remove(row.value.trim())}
          onClick={onRemove}
        >
          <Trash2 aria-hidden="true" />
        </Button>
      </div>
      {tip ? (
        <p
          id={`${id}-tip`}
          className={cn("text-xs", error ? "text-destructive" : "text-status-partial-fg")}
        >
          {tip}
        </p>
      ) : null}
      <fieldset
        className="flex flex-wrap items-center gap-x-1.5 gap-y-1"
        aria-invalid={scopesError ? true : undefined}
        aria-describedby={scopeTip ? `${id}-scopes-tip` : undefined}
      >
        <legend className="sr-only">{T.scopesLegend(kind, index + 1)}</legend>
        <span aria-hidden="true" className="eyebrow mr-1">
          {T.scopesTitle}
        </span>
        {ALL_SCOPES.map((scope) => {
          const on = row.scopes.includes(scope);
          const danger = scope === "PERSONAL_WEBHOOKS";
          return (
            <label
              key={scope}
              title={T.scopeHelp[scope]}
              className={cn(
                "inline-flex cursor-pointer items-center gap-1.5 rounded-sm border px-2 py-0.5 text-xs transition-colors",
                "has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-ring",
                on && !danger && "border-primary/40 bg-accent text-foreground",
                on && danger && "hatch-insecure border-status-insecure/60 text-status-insecure-fg",
                !on && "border-border text-muted-foreground hover:text-foreground",
              )}
            >
              <input
                type="checkbox"
                className={cn("size-3.5", danger ? "accent-destructive" : "accent-primary")}
                checked={on}
                onChange={(e) => {
                  onChange({ scopes: toggleScope(row.scopes, scope, e.target.checked) });
                }}
              />
              {danger ? <ShieldAlert aria-hidden="true" className="size-3" /> : null}
              {T.scopeNames[scope]}
            </label>
          );
        })}
      </fieldset>
      {scopeTip ? (
        <p
          id={`${id}-scopes-tip`}
          className={cn("text-xs", scopesError ? "text-destructive" : "text-status-partial-fg")}
        >
          {scopeTip}
        </p>
      ) : null}
    </li>
  );
}
