import type {
  RunnerDeniedCategory,
  RunnerTargetWarning,
  RunnerTargetsKey,
} from "@/graphql/gen/graphql";
import { defineMessages } from "@/lib/i18n";

/** Strings of the runner part of the Network page (D-223). */
export const RT = defineMessages({
  en: {
    title: "Database servers (runner)",
    lead: "Database servers and SSH bastions are reached by the runner, never by bekup itself. The runner allows only what the [targets] section of its own runner.toml lists; everything else is refused.",
    readOnly: "Read-only",
    editNote:
      "Edited in runner.toml [targets] on the runner host; restart the runner to apply. The runner has no access to bekup's database, so this page cannot change it.",
    guide: "Runner target allowlist",
    loading: "Asking the runner…",
    unavailableTitle: "The runner's list is not available right now",
    unavailableLead:
      "The rest of this page works as usual. The runner keeps enforcing its list; only this view is missing.",
    retry: "Ask again",
    allowTitle: "Allowed",
    allowLead:
      "Database servers and bastions the runner may connect to: an address in one of the networks, or one of the host names.",
    denyTitle: "Refused first",
    denyLead:
      "Checked before the allowed lists: a match here is refused even if it is also allowed.",
    cidrs: "Networks (allow_cidrs)",
    hosts: "Host names (allow_hosts)",
    denyCidrs: "Networks (deny_cidrs)",
    denyHosts: "Host names (deny_hosts)",
    noAllowCidrs:
      "No CIDR allowed — every database server is refused unless a host name below allows it.",
    noAllowHosts: "No host name allowed.",
    nothingAllowedTag: "Refuses everything",
    nothingAllowed:
      "Nothing is allowed: the runner refuses every database server and SSH bastion until runner.toml lists them.",
    noDenyCidrs: "No networks refused beyond the always-refused ranges.",
    noDenyHosts: "No host names refused.",
    bastionsTitle: "SSH bastions",
    bastionsLead:
      "What each bastion may forward to (bastion_targets). The bastion itself must also be allowed above.",
    bastion: "Bastion",
    bastionTargets: "May forward to",
    bastionsCaption: "SSH bastions and the targets each may forward to",
    noBastions:
      "No bastion is restricted: an allowed SSH bastion may forward to any host it reaches; only the refused lists apply behind it.",
    ownTitle: "The runner's own networks",
    ownLead: "Always refused, so a connection can never point back at the runner or another run.",
    pool: "Run subnet pool (docker.run_subnet_pool)",
    poolNote: "Every run gets its own network inside it.",
    control: "Control network and the runner's addresses",
    controlPending:
      "Not known yet: the runner has not finished connecting to Docker. Ask again in a moment.",
    noPool: "Not reported.",
    deniedTitle: "Always refused",
    deniedLead:
      "No runner.toml entry can open these. Addresses inside other Docker networks on the runner host are refused too, unless an allow_cidrs entry inside that network allows them.",
    categories: {
      LOOPBACK: "Loopback",
      UNSPECIFIED: "Unspecified and “this network”",
      LINK_LOCAL: "Link-local",
      CLOUD_METADATA: "Cloud metadata endpoints",
      MULTICAST_BROADCAST: "Multicast and broadcast",
      RESERVED: "Reserved",
      IPV4_IN_IPV6: "IPv4-compatible and IPv4-translated IPv6",
      TEREDO: "Teredo",
      EMBEDDED_DENIED_IPV4: "NAT64 and 6to4 forms of an always-refused IPv4 address",
      NON_GLOBAL_IPV6: "Other IPv6 outside global unicast (2000::/3)",
    } satisfies Record<RunnerDeniedCategory, string>,
    warningsTitle: "Runner warnings",
    warnings: {
      EMPTY_ALLOWLIST:
        "allow_cidrs and allow_hosts are empty: the runner refuses every database server and SSH bastion.",
      BASTIONS_UNRESTRICTED:
        "bastion_targets is empty: an allowed SSH bastion may forward to any host it reaches. List each bastion's databases in bastion_targets.",
    } satisfies Record<RunnerTargetWarning, string>,
    envTitle: "Set by environment variables",
    envLead: (keys: string) =>
      `These keys come from BEKUP_TARGETS_* variables of the runner, not from runner.toml: ${keys}.`,
    keys: {
      ALLOW_CIDRS: "allow_cidrs",
      ALLOW_HOSTS: "allow_hosts",
      DENY_CIDRS: "deny_cidrs",
      DENY_HOSTS: "deny_hosts",
      BASTION_TARGETS: "bastion_targets",
    } satisfies Record<RunnerTargetsKey, string>,
    truncated: "The runner reports at most 256 entries per list; runner.toml holds more.",
  },
  nl: {
    title: "Databaseservers (runner)",
    lead: "Databaseservers en SSH-bastionhosts worden door de runner bereikt, nooit door bekup zelf. De runner staat alleen toe wat in de sectie [targets] van zijn eigen runner.toml staat; al het andere wordt geweigerd.",
    readOnly: "Alleen-lezen",
    editNote:
      "Wordt bewerkt in runner.toml [targets] op de runnerhost; herstart de runner om het toe te passen. De runner heeft geen toegang tot de database van bekup, dus deze pagina kan dit niet wijzigen.",
    guide: "Toegestane lijst van runnerdoelen",
    loading: "De runner wordt gevraagd…",
    unavailableTitle: "De lijst van de runner is nu niet beschikbaar",
    unavailableLead:
      "De rest van deze pagina werkt gewoon. De runner blijft zijn lijst toepassen; alleen deze weergave ontbreekt.",
    retry: "Opnieuw vragen",
    allowTitle: "Toegestaan",
    allowLead:
      "Databaseservers en bastionhosts waarmee de runner verbinding mag maken: een adres in een van de netwerken, of een van de hostnamen.",
    denyTitle: "Eerst geweigerd",
    denyLead:
      "Wordt vóór de toegestane lijsten gecontroleerd: wat hier overeenkomt, wordt geweigerd, ook als het ook is toegestaan.",
    cidrs: "Netwerken (allow_cidrs)",
    hosts: "Hostnamen (allow_hosts)",
    denyCidrs: "Netwerken (deny_cidrs)",
    denyHosts: "Hostnamen (deny_hosts)",
    noAllowCidrs:
      "Geen CIDR toegestaan — elke databaseserver wordt geweigerd, tenzij een hostnaam hieronder hem toestaat.",
    noAllowHosts: "Geen hostnaam toegestaan.",
    nothingAllowedTag: "Weigert alles",
    nothingAllowed:
      "Er is niets toegestaan: de runner weigert elke databaseserver en SSH-bastionhost tot runner.toml ze vermeldt.",
    noDenyCidrs: "Geen netwerken geweigerd naast de bereiken die altijd worden geweigerd.",
    noDenyHosts: "Geen hostnamen geweigerd.",
    bastionsTitle: "SSH-bastionhosts",
    bastionsLead:
      "Waarnaar elke bastionhost mag doorsturen (bastion_targets). De bastionhost zelf moet hierboven ook zijn toegestaan.",
    bastion: "Bastionhost",
    bastionTargets: "Mag doorsturen naar",
    bastionsCaption: "SSH-bastionhosts en de doelen waarnaar elk mag doorsturen",
    noBastions:
      "Geen enkele bastionhost is beperkt: een toegestane SSH-bastionhost mag doorsturen naar elke host die hij bereikt; daarachter gelden alleen de geweigerde lijsten.",
    ownTitle: "De eigen netwerken van de runner",
    ownLead:
      "Altijd geweigerd, zodat een verbinding nooit terug kan wijzen naar de runner of een andere run.",
    pool: "Subnetpool voor runs (docker.run_subnet_pool)",
    poolNote: "Elke run krijgt daarin een eigen netwerk.",
    control: "Beheernetwerk en de adressen van de runner",
    controlPending:
      "Nog niet bekend: de runner heeft de verbinding met Docker nog niet voltooid. Vraag het zo opnieuw.",
    noPool: "Niet gemeld.",
    deniedTitle: "Altijd geweigerd",
    deniedLead:
      "Geen enkele vermelding in runner.toml kan deze openzetten. Adressen in andere Docker-netwerken op de runnerhost worden ook geweigerd, tenzij een allow_cidrs-vermelding binnen dat netwerk ze toestaat.",
    categories: {
      LOOPBACK: "Loopback",
      UNSPECIFIED: "Niet-gespecificeerd en „dit netwerk”",
      LINK_LOCAL: "Link-local",
      CLOUD_METADATA: "Metadata-eindpunten van clouds",
      MULTICAST_BROADCAST: "Multicast en broadcast",
      RESERVED: "Gereserveerd",
      IPV4_IN_IPV6: "IPv4-compatibele en IPv4-vertaalde IPv6",
      TEREDO: "Teredo",
      EMBEDDED_DENIED_IPV4: "NAT64- en 6to4-vormen van een IPv4-adres dat altijd wordt geweigerd",
      NON_GLOBAL_IPV6: "Overige IPv6 buiten global unicast (2000::/3)",
    },
    warningsTitle: "Waarschuwingen van de runner",
    warnings: {
      EMPTY_ALLOWLIST:
        "allow_cidrs en allow_hosts zijn leeg: de runner weigert elke databaseserver en SSH-bastionhost.",
      BASTIONS_UNRESTRICTED:
        "bastion_targets is leeg: een toegestane SSH-bastionhost mag doorsturen naar elke host die hij bereikt. Vermeld de databases van elke bastionhost in bastion_targets.",
    },
    envTitle: "Ingesteld via omgevingsvariabelen",
    envLead: (keys: string) =>
      `Deze sleutels komen uit BEKUP_TARGETS_*-variabelen van de runner, niet uit runner.toml: ${keys}.`,
    keys: {
      ALLOW_CIDRS: "allow_cidrs",
      ALLOW_HOSTS: "allow_hosts",
      DENY_CIDRS: "deny_cidrs",
      DENY_HOSTS: "deny_hosts",
      BASTION_TARGETS: "bastion_targets",
    },
    truncated: "De runner meldt maximaal 256 vermeldingen per lijst; runner.toml bevat er meer.",
  },
});
