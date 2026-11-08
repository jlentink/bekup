import { defineMessages, plural, type Messages } from "@/lib/i18n";

/** Strings of the API access UI (D-220), in English and Dutch (D-235). */

/** "1 day" / "3 days": the count with the noun in the right form. */
const count = (n: number, one: string, many: string) =>
  `${String(n)} ${plural(n, { one, other: many })}`;

const en = {
  panel: {
    title: "API access",
    lead: "Let a CI pipeline start this job's backup and wait for the result, for example before a deployment migrates the database. Each token is shown once, expires (unless created without an expiry) and can be revoked at any time.",
    create: "Create API token",
    empty: "No API tokens for this job yet.",
    loadFailed: "The API tokens could not be loaded.",
    examples: "Use it from CI",
    examplesLead: (variable: string) =>
      `Store the token as a CI secret named ${variable}. The examples read it from that variable; never paste the token into a script or URL.`,
    revokedHeading: (n: number) => `Revoked or expired (${String(n)})`,
  },
  page: {
    eyebrow: "Administration · External API",
    title: "API access",
    lead: "Every external API token of this instance: which jobs it may start, where it was last used from and which addresses it is limited to. Revoke tokens you do not recognize.",
    empty: "No API tokens yet",
    emptyLead:
      "Admins and backup owners create tokens on a job's page (API access) or here. A token lets a CI pipeline start backups of its jobs and follow them.",
    guide: "External API guide",
  },
  list: {
    label: "API tokens",
    prefix: "Token",
    created: (at: string, by: string | null) => (by ? `Created ${at} by ${by}` : `Created ${at}`),
    createdByYou: "you",
    expiry: "Expiry",
    expires: (at: string) => `Expires ${at}`,
    expired: (at: string) => `Expired ${at}`,
    neverExpires: "Never expires",
    neverExpiresHint: "Valid until revoked",
    revoked: (at: string) => `Revoked ${at}`,
    states: {
      active: "Active",
      expiring: "Expires soon",
      expired: "Expired",
      revoked: "Revoked",
    },
    expiringTitle: (days: number) =>
      days <= 0
        ? "Expires within a day: create a new token and update the CI secret."
        : `Expires in ${count(days, "day", "days")}: create a new token and update the CI secret.`,
    jobs: "Jobs",
    otherJobs: (n: number) =>
      n === 0 ? "Only this job" : `+ ${count(n, "other job", "other jobs")}`,
    hiddenJobs: (n: number) => `+ ${count(n, "job", "jobs")} you cannot see`,
    deletedJob: "deleted",
    addresses: "Allowed from",
    lastUse: "Last seen",
    never: "Never used",
    seen: (when: string, ip: string | null | undefined) => (ip ? `${when} from ${ip}` : when),
    refused: "Refused: address outside the allowlist",
    refusedTitle:
      "The last attempt came from an address outside the token's allowlist and was refused. If you do not recognize the address, revoke the token.",
    actions: (name: string) => `Actions for ${name}`,
    editAddresses: "Edit addresses",
    revoke: "Revoke",
    revokeTitle: (name: string) => `Revoke ${name}?`,
    revokeLead:
      "The token stops working at once: the next CI call gets 401. This cannot be undone; create a new token if you need access again.",
    revokeConfirm: "Revoke token",
    revokedToast: "API token revoked.",
  },
  addresses: {
    legend: "Client addresses",
    label: "Allowed networks",
    hint: (max: number) =>
      `One IPv4 or IPv6 address or network per line (for example 203.0.113.7 or 203.0.113.0/24), at most ${String(max)}. Calls from anywhere else are refused, and the admins and the creator are alerted.`,
    selfHint: "To find the address bekup sees for your runner, call",
    selfHintAfter: "with any working token and read clientIp.",
    any: "Allow from any address",
    anyWarning:
      "Without an allowlist, anyone who obtains the token can start this job's backups from anywhere. The token keeps a permanent warning badge, and the admins are alerted. Prefer CI runners with fixed addresses.",
    hostedRunners:
      "Hosted CI runners (such as GitHub-hosted runners) use large, changing address ranges: an allowlist of those keeps out little.",
  },
  create: {
    title: "Create an API token",
    lead: "The token can start backups of the selected jobs through the external API. It is shown once after you confirm your identity.",
    name: "Name",
    nameHint: "Where it is used, e.g. “deploy pipeline (GitLab)”.",
    nameMissing: "Enter a name (1 to 100 characters).",
    jobs: "Jobs",
    jobsHint: "The token can start backups of these jobs only.",
    jobsFilter: "Search jobs",
    jobsTruncated: "More jobs match: search by name to find the others.",
    jobsNoMatch: "No job matches the search.",
    jobsMissing: "Choose at least one job.",
    jobsLoading: "Loading jobs…",
    noJobs: "No jobs you may use.",
    expiry: "Expires after",
    days: (n: number) => `${String(n)} days`,
    never: "Never",
    expiryHint: "Required. Rotate the token before it expires.",
    neverHint:
      "The token stays valid until it is revoked. Admins are alerted and the token is permanently flagged; prefer an expiry you rotate.",
    submit: "Create token",
  },
  edit: {
    title: (name: string) => `Addresses of ${name}`,
    lead: "Changes apply to the next call and are audited; allowing any address alerts the admins.",
    submit: "Save addresses",
    saved: "Addresses saved.",
  },
  showOnce: {
    eyebrow: "Shown once",
    title: "Save the API token",
    lead: (variable: string) =>
      `Store it now as a CI secret named ${variable}. bekup keeps only a hash: you won't see this token again, and a lost token cannot be recovered (create a new one).`,
    tokenLabel: "API token",
    copy: "Copy token",
    copied: "Token copied to the clipboard.",
    copyFailed: "Copying failed. Select the token and copy it by hand.",
    acknowledge: "I stored the token as a CI secret; I won't see it again",
    done: "Done",
  },
  disabled: {
    title: "The external API is not enabled on this server",
    lead: "CI pipelines cannot call it yet. An administrator enables it on the host with",
    command: "docker compose run --rm setup upgrade --external-api",
    after:
      " and the steps it prints. Tokens created now work once it runs; the examples appear here then.",
    guide: "External API guide",
  },
  examples: {
    region: "curl examples",
    start: "1. Start a backup",
    startLead: "Answers 202 with a requestId. A run of the job already in progress gives 409.",
    status: "2. Check the status",
    statusLead:
      "Set REQUEST_ID to the requestId from step 1. Poll until done is true: state shows where the run is (pending, queued, running, retrying, then success, partial, failed, canceled or skipped), and success is true only when every database was backed up (partial is not a success).",
    script: "3. Complete CI script",
    scriptLead:
      "Starts the backup, polls every 15 seconds until done and exits non-zero unless success. Needs curl and jq.",
    copy: (what: string) => `Copy: ${what}`,
    copied: "Copied to the clipboard.",
    copyFailed: "Copying failed. Select the text and copy it by hand.",
  },
};

const nl: Messages<typeof en> = {
  panel: {
    title: "API-toegang",
    lead: "Laat een CI-pipeline de back-up van deze taak starten en op het resultaat wachten, bijvoorbeeld voordat een deployment de database migreert. Elk token wordt één keer getoond, verloopt (tenzij het zonder vervaldatum is aangemaakt) en kan op elk moment worden ingetrokken.",
    create: "API-token aanmaken",
    empty: "Nog geen API-tokens voor deze taak.",
    loadFailed: "De API-tokens konden niet worden geladen.",
    examples: "Gebruiken vanuit CI",
    examplesLead: (variable: string) =>
      `Sla het token op als CI-secret met de naam ${variable}. De voorbeelden lezen het uit die variabele; plak het token nooit in een script of URL.`,
    revokedHeading: (n: number) => `Ingetrokken of verlopen (${String(n)})`,
  },
  page: {
    eyebrow: "Beheer · Externe API",
    title: "API-toegang",
    lead: "Elk extern API-token van deze instantie: welke taken het mag starten, waarvandaan het het laatst is gebruikt en tot welke adressen het beperkt is. Trek tokens in die je niet herkent.",
    empty: "Nog geen API-tokens",
    emptyLead:
      "Beheerders en eigenaren maken tokens aan op de pagina van een taak (API-toegang) of hier. Met een token kan een CI-pipeline back-ups van zijn taken starten en volgen.",
    guide: "Handleiding externe API",
  },
  list: {
    label: "API-tokens",
    prefix: "Token",
    created: (at: string, by: string | null) =>
      by ? `Aangemaakt ${at} door ${by}` : `Aangemaakt ${at}`,
    createdByYou: "jou",
    expiry: "Vervaldatum",
    expires: (at: string) => `Verloopt ${at}`,
    expired: (at: string) => `Verlopen ${at}`,
    neverExpires: "Verloopt nooit",
    neverExpiresHint: "Geldig tot het wordt ingetrokken",
    revoked: (at: string) => `Ingetrokken ${at}`,
    states: {
      active: "Actief",
      expiring: "Verloopt binnenkort",
      expired: "Verlopen",
      revoked: "Ingetrokken",
    },
    expiringTitle: (days: number) =>
      days <= 0
        ? "Verloopt binnen een dag: maak een nieuw token aan en werk het CI-secret bij."
        : `Verloopt over ${count(days, "dag", "dagen")}: maak een nieuw token aan en werk het CI-secret bij.`,
    jobs: "Taken",
    otherJobs: (n: number) =>
      n === 0 ? "Alleen deze taak" : `+ ${count(n, "andere taak", "andere taken")}`,
    hiddenJobs: (n: number) =>
      `+ ${count(n, "taak die je niet kunt zien", "taken die je niet kunt zien")}`,
    deletedJob: "verwijderd",
    addresses: "Toegestaan vanaf",
    lastUse: "Laatst gezien",
    never: "Nooit gebruikt",
    seen: (when: string, ip: string | null | undefined) => (ip ? `${when} vanaf ${ip}` : when),
    refused: "Geweigerd: adres buiten de toegestane adressen",
    refusedTitle:
      "De laatste poging kwam van een adres buiten de toegestane adressen van het token en is geweigerd. Herken je het adres niet, trek het token dan in.",
    actions: (name: string) => `Acties voor ${name}`,
    editAddresses: "Adressen bewerken",
    revoke: "Intrekken",
    revokeTitle: (name: string) => `${name} intrekken?`,
    revokeLead:
      "Het token werkt meteen niet meer: de volgende CI-aanroep krijgt 401. Dit kan niet ongedaan worden gemaakt; maak een nieuw token aan als je weer toegang nodig hebt.",
    revokeConfirm: "Token intrekken",
    revokedToast: "API-token ingetrokken.",
  },
  addresses: {
    legend: "Clientadressen",
    label: "Toegestane netwerken",
    hint: (max: number) =>
      `Eén IPv4- of IPv6-adres of -netwerk per regel (bijvoorbeeld 203.0.113.7 of 203.0.113.0/24), maximaal ${String(max)}. Aanroepen van elders worden geweigerd, en de beheerders en de maker krijgen een melding.`,
    selfHint: "Om het adres te vinden dat bekup voor je runner ziet, roep je",
    selfHintAfter: "aan met een werkend token en lees je clientIp.",
    any: "Vanaf elk adres toestaan",
    anyWarning:
      "Zonder toegestane adressen kan iedereen die het token in handen krijgt overal vandaan de back-ups van deze taak starten. Het token houdt een permanente waarschuwing en de beheerders krijgen een melding. Gebruik liever CI-runners met vaste adressen.",
    hostedRunners:
      "Gehoste CI-runners (zoals de runners van GitHub) gebruiken grote, wisselende adresbereiken: als je die toestaat, houd je weinig buiten.",
  },
  create: {
    title: "API-token aanmaken",
    lead: "Met het token kunnen back-ups van de gekozen taken worden gestart via de externe API. Het wordt één keer getoond nadat je je identiteit hebt bevestigd.",
    name: "Naam",
    nameHint: "Waar het wordt gebruikt, bijv. „deploy-pipeline (GitLab)”.",
    nameMissing: "Vul een naam in (1 tot 100 tekens).",
    jobs: "Taken",
    jobsHint: "Het token kan alleen back-ups van deze taken starten.",
    jobsFilter: "Taken zoeken",
    jobsTruncated: "Er zijn meer overeenkomende taken: zoek op naam om de andere te vinden.",
    jobsNoMatch: "Geen taak komt overeen met de zoekopdracht.",
    jobsMissing: "Kies minstens één taak.",
    jobsLoading: "Taken laden…",
    noJobs: "Geen taken die je mag gebruiken.",
    expiry: "Verloopt na",
    days: (n: number) => `${String(n)} dagen`,
    never: "Nooit",
    expiryHint: "Verplicht. Vervang het token voordat het verloopt.",
    neverHint:
      "Het token blijft geldig tot het wordt ingetrokken. Beheerders krijgen een melding en het token wordt permanent gemarkeerd; kies liever een vervaldatum en vervang het op tijd.",
    submit: "Token aanmaken",
  },
  edit: {
    title: (name: string) => `Adressen van ${name}`,
    lead: "Wijzigingen gelden vanaf de volgende aanroep en komen in de auditlog; elk adres toestaan geeft de beheerders een melding.",
    submit: "Adressen opslaan",
    saved: "Adressen opgeslagen.",
  },
  showOnce: {
    eyebrow: "Eén keer getoond",
    title: "Het API-token opslaan",
    lead: (variable: string) =>
      `Sla het nu op als CI-secret met de naam ${variable}. bekup bewaart alleen een hash: je ziet dit token niet meer terug en een verloren token kan niet worden hersteld (maak een nieuw aan).`,
    tokenLabel: "API-token",
    copy: "Token kopiëren",
    copied: "Token naar het klembord gekopieerd.",
    copyFailed: "Kopiëren mislukt. Selecteer het token en kopieer het met de hand.",
    acknowledge: "Ik heb het token als CI-secret opgeslagen; ik zie het niet meer terug",
    done: "Klaar",
  },
  disabled: {
    title: "De externe API is niet ingeschakeld op deze server",
    lead: "CI-pipelines kunnen hem nog niet aanroepen. Een beheerder schakelt hem op de host in met",
    command: "docker compose run --rm setup upgrade --external-api",
    after:
      " en de stappen die dat toont. Tokens die je nu aanmaakt, werken zodra dat is gedaan; de voorbeelden verschijnen dan hier.",
    guide: "Handleiding externe API",
  },
  examples: {
    region: "curl-voorbeelden",
    start: "1. Een back-up starten",
    startLead:
      "Antwoordt 202 met een requestId. Is er al een run van de taak bezig, dan krijg je 409.",
    status: "2. De status controleren",
    statusLead:
      "Zet REQUEST_ID op de requestId uit stap 1. Vraag de status op tot done true is: state laat zien waar de run is (pending, queued, running, retrying, daarna success, partial, failed, canceled of skipped), en success is alleen true als van elke database een back-up is gemaakt (partial telt niet als geslaagd).",
    script: "3. Volledig CI-script",
    scriptLead:
      "Start de back-up, vraagt elke 15 seconden de status op tot done en stopt met een foutcode tenzij success. Vereist curl en jq.",
    copy: (what: string) => `Kopiëren: ${what}`,
    copied: "Naar het klembord gekopieerd.",
    copyFailed: "Kopiëren mislukt. Selecteer de tekst en kopieer die met de hand.",
  },
};

export const T = defineMessages({ en, nl });
