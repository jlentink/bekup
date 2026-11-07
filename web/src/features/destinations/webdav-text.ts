import type {
  WebdavFlavor,
  WebdavOverwriteProbe,
  WebdavServerKind,
  WebdavTrust,
  WebdavUploadMode,
} from "@/graphql/gen/graphql";
import { defineMessages, type Messages } from "@/lib/i18n";

/**
 * Strings of the WebDAV destination UI (D-222): form, detail page, test
 * details and the certificate pin flow, in English and Dutch (D-235).
 */

const en = {
  flavor: {
    GENERIC: "Generic WebDAV",
    NEXTCLOUD: "Nextcloud / ownCloud",
  } as Record<WebdavFlavor, string>,
  flavorDescription: {
    GENERIC:
      "Plain WebDAV (NAS, Apache, nginx, rclone, Caddy, Hetzner Storage Box): every file is one streaming PUT.",
    NEXTCLOUD:
      "Nextcloud or ownCloud, TransIP STACK too: large files go up in 64 MiB chunks, so PHP and proxy upload limits never cut off a multi-GB dump.",
  } as Record<WebdavFlavor, string>,
  urlHelp: {
    GENERIC: {
      description:
        "The existing folder that holds the backups, for example https://u123.your-storagebox.de/backups.",
      placeholder: "https://u123.your-storagebox.de/backups",
    },
    NEXTCLOUD: {
      description:
        "The server address, for example https://cloud.example.com or https://name.stackstorage.com. bekup finds the user's files below it.",
      placeholder: "https://cloud.example.com",
    },
  } as Record<WebdavFlavor, { description: string; placeholder: string }>,
  pathHelp: {
    GENERIC: "Optional folder below the URL; created if missing. Empty: the URL itself.",
    NEXTCLOUD: "Optional folder below the user's files, e.g. Backups/bekup; created if missing.",
  } as Record<WebdavFlavor, string>,
  trust: {
    SYSTEM: "System roots",
    CA_BUNDLE: "Own CA bundle",
    PINNED: "Pinned certificate",
    PLAIN_HTTP: "Plain HTTP (dev mode)",
  } as Record<WebdavTrust, string>,
  trustDescription: {
    SYSTEM: "The certificate must chain to a public root CA the server trusts and name the host.",
    CA_BUNDLE:
      "The certificate must chain to the destination's own CA bundle (system roots unused).",
    PINNED:
      "Only the pinned certificate's key is accepted: no system roots, no CA bundle. A renewal with a new key needs a new pin.",
    PLAIN_HTTP:
      "No TLS at all: the password and the encrypted backups travel unprotected. Accepted in dev mode only.",
  } as Record<WebdavTrust, string>,
  server: {
    NEXTCLOUD: "Nextcloud",
    OWNCLOUD: "ownCloud",
  } as Record<WebdavServerKind, string>,
  upload: {
    SINGLE: "One streaming PUT",
    CHUNKED: "Chunked upload (Nextcloud/ownCloud API)",
  } as Record<WebdavUploadMode, string>,
  overwrite: {
    REFUSED: "Refused: existing files are never replaced",
    IGNORED: "Ignored: the server replaces existing files",
  } as Record<WebdavOverwriteProbe, string>,
  wt: {
    form: {
      serverLegend: "Server",
      serverLead:
        "Plain http is refused (it works in dev mode only): the password is sent with HTTP Basic auth, over HTTPS only.",
      pinNote: "Changing the URL removes the pinned certificate; pin it again after the next test.",
      url: "URL",
      flavor: "Server type",
      path: "Folder",
      signInLegend: "Sign-in",
      signInLead:
        "HTTP Basic auth. The password is write-only: stored encrypted, never shown again. For Nextcloud and ownCloud use an app password (Settings → Security), never the account password.",
      username: "Username",
      password: "Password",
      newPassword: "New password",
      passwordHint: "An app password for Nextcloud/ownCloud.",
      reentry:
        "The URL, server type or username changed. Enter the password again: bekup never sends a stored password to a new endpoint.",
      tlsLegend: "TLS and timeouts",
      tlsLead:
        "A self-signed certificate needs no CA bundle: test the destination, then pin the certificate it shows.",
      caBundle: "Custom CA bundle (PEM, optional)",
      caBundleHint:
        "Replaces the system roots for this server (a NAS or private CA). Unused while a certificate is pinned.",
      timeout: "Request timeout (seconds, optional)",
      timeoutHint: "One metadata request; 5 to 600, default 60. Uploads are not limited by it.",
    },
    errors: {
      url: "Enter the https:// URL.",
      urlScheme: "Enter an https:// URL (http only in dev mode).",
      username: "Enter the username.",
      usernameColon: "The username cannot contain “:”.",
      reentry:
        "The URL, server type or username changed: enter the password again. A stored password is never sent to a new endpoint.",
      caBundle: "Paste the CA bundle as PEM.",
      timeout: "Enter a whole number of seconds between 5 and 600.",
      path: "Use a folder without “..”.",
    },
    facts: {
      url: "URL",
      flavor: "Server type",
      username: "Username",
      path: "Folder",
      pathBase: "(the URL itself)",
      pathFiles: "(the user's files)",
      password: "Password",
      timeout: "Request timeout",
      timeoutValue: (n: number | null | undefined) =>
        n == null ? "60 s (default)" : `${String(n)} s`,
      caBundle: "Custom CA bundle",
      copyCaBundle: "Copy the CA bundle",
    },
    tls: {
      title: "TLS",
      lead: "How bekup verifies the server's certificate before it sends the password.",
      trust: "Verified against",
      pinned: "Pinned certificate",
      notPinnedHint:
        "To use a self-signed certificate, run the test above and pin the certificate it shows.",
      removePin: "Remove pin",
      removeTitle: "Remove the certificate pin?",
      removeLead:
        "bekup then verifies the server against the system roots again, or against the destination's CA bundle if one is set. A self-signed certificate fails from then on until you pin it again. Removing the pin is audited and raises a security alert.",
      removed: "Certificate pin removed.",
    },
    cert: {
      spki: "Public key (SPKI SHA-256)",
      spkiShort: "SPKI SHA-256",
      fingerprint: "Certificate SHA-256",
      valid: "Valid",
      validRange: (from: string, to: string) => `${from} → ${to}`,
      pinnedAt: "Pinned",
      selfSigned: "Self-signed",
      caIssued: "CA-issued",
      expired: "Expired",
      notYet: "Not yet valid",
      copySpki: "Copy the SPKI SHA-256 (hex)",
      copyFingerprint: "Copy the certificate SHA-256",
    },
    test: {
      heading: "TLS and server",
      certificate: "Certificate",
      noCertificate: "None seen (unreachable or plain http).",
      trust: "Verification",
      trusted: "Trusted",
      untrusted: "Not trusted",
      pinMatches: "Matches the pin",
      pinMismatch: "Does not match the pin",
      server: "Server",
      serverUnknown: "Generic WebDAV",
      uploadMode: "Uploads",
      overwrite: "Overwrite protection",
      notRun: "Did not run",
      suggestFlavor: (server: string) =>
        `This looks like ${server}: use the Nextcloud / ownCloud server type, so large dumps go up in chunks.`,
      suggestGeneric:
        "The server did not answer like Nextcloud or ownCloud (no status.php). If it is a plain WebDAV server, use the Generic WebDAV server type.",
      ignoredTitle: "This server would replace existing backups",
      ignoredBody:
        "bekup asked the server to move a file onto an existing one with “Overwrite: F”, which a WebDAV server must refuse. This server ignored it and replaced the file, so a retried or concurrent upload could silently overwrite a stored backup. bekup refuses to write to it. Use another server, or fix the server's WebDAV module, and test again.",
      untrustedHint:
        "The certificate did not pass verification. If it is the server's own self-signed certificate, compare its fingerprint and pin it.",
      mismatchHint:
        "The server presents a different key than the pinned one. Pin it only if the certificate was replaced on purpose: an unexpected change can mean someone is intercepting the connection.",
      pin: "Pin this certificate",
      pinnedNow: "Certificate pinned. Test again to verify the connection with the pin.",
    },
    pin: {
      title: "Pin this certificate",
      lead: "After pinning, bekup accepts only this certificate's key for the server: no system roots, no CA bundle. Compare the fingerprint with the server out of band first.",
      compareHint: "On the server, for example:",
      command:
        "openssl x509 -in cert.pem -pubkey -noout | openssl pkey -pubin -outform der | openssl dgst -sha256",
      browserHint:
        "Browsers and openssl x509 -fingerprint -sha256 show the certificate SHA-256 instead.",
      renewal:
        "When the certificate is renewed with a new key (Let's Encrypt does that by default), backups fail until you pin the new one.",
      compared: "I compared the fingerprint with the server out of band and it matches.",
      confirm: "Pin certificate",
      cancel: "Cancel",
      pinned: "Certificate pinned.",
      conflict:
        "The server now presents a different certificate than the one shown here. Nothing was pinned: test the destination again and compare the new fingerprint.",
    },
  },
};

const nl: Messages<typeof en> = {
  flavor: {
    GENERIC: "Generieke WebDAV",
    NEXTCLOUD: "Nextcloud / ownCloud",
  },
  flavorDescription: {
    GENERIC:
      "Gewone WebDAV (NAS, Apache, nginx, rclone, Caddy, Hetzner Storage Box): elk bestand gaat in één streamende PUT.",
    NEXTCLOUD:
      "Nextcloud of ownCloud, ook TransIP STACK: grote bestanden gaan in stukken van 64 MiB, zodat uploadlimieten van PHP en proxy een dump van meerdere GB nooit afbreken.",
  },
  urlHelp: {
    GENERIC: {
      description:
        "De bestaande map voor de back-ups, bijvoorbeeld https://u123.your-storagebox.de/backups.",
      placeholder: "https://u123.your-storagebox.de/backups",
    },
    NEXTCLOUD: {
      description:
        "Het serveradres, bijvoorbeeld https://cloud.example.com of https://name.stackstorage.com. bekup vindt de bestanden van de gebruiker daaronder.",
      placeholder: "https://cloud.example.com",
    },
  },
  pathHelp: {
    GENERIC: "Optionele map onder de URL; wordt aangemaakt als hij ontbreekt. Leeg: de URL zelf.",
    NEXTCLOUD:
      "Optionele map onder de bestanden van de gebruiker, bijv. Backups/bekup; wordt aangemaakt als hij ontbreekt.",
  },
  trust: {
    SYSTEM: "Systeemroots",
    CA_BUNDLE: "Eigen CA-bundel",
    PINNED: "Vastgepind certificaat",
    PLAIN_HTTP: "Gewone HTTP (dev-modus)",
  },
  trustDescription: {
    SYSTEM:
      "Het certificaat moet leiden naar een publieke root-CA die de server vertrouwt en de host noemen.",
    CA_BUNDLE:
      "Het certificaat moet leiden naar de eigen CA-bundel van de bestemming (systeemroots niet gebruikt).",
    PINNED:
      "Alleen de sleutel van het vastgepinde certificaat wordt geaccepteerd: geen systeemroots, geen CA-bundel. Een vernieuwing met een nieuwe sleutel vraagt een nieuwe pin.",
    PLAIN_HTTP:
      "Helemaal geen TLS: het wachtwoord en de versleutelde back-ups gaan onbeschermd over het netwerk. Alleen toegestaan in dev-modus.",
  },
  server: {
    NEXTCLOUD: "Nextcloud",
    OWNCLOUD: "ownCloud",
  },
  upload: {
    SINGLE: "Eén streamende PUT",
    CHUNKED: "Upload in stukken (Nextcloud/ownCloud-API)",
  },
  overwrite: {
    REFUSED: "Geweigerd: bestaande bestanden worden nooit vervangen",
    IGNORED: "Genegeerd: de server vervangt bestaande bestanden",
  },
  wt: {
    form: {
      serverLegend: "Server",
      serverLead:
        "Gewone http wordt geweigerd (werkt alleen in dev-modus): het wachtwoord gaat met HTTP Basic-authenticatie mee, alleen via HTTPS.",
      pinNote:
        "Als je de URL wijzigt, verdwijnt het vastgepinde certificaat; pin het opnieuw na de volgende test.",
      url: "URL",
      flavor: "Servertype",
      path: "Map",
      signInLegend: "Inloggen",
      signInLead:
        "HTTP Basic-authenticatie. Het wachtwoord is alleen-schrijven: versleuteld opgeslagen en nooit meer getoond. Gebruik voor Nextcloud en ownCloud een app-wachtwoord (Instellingen → Beveiliging), nooit het wachtwoord van het account.",
      username: "Gebruikersnaam",
      password: "Wachtwoord",
      newPassword: "Nieuw wachtwoord",
      passwordHint: "Een app-wachtwoord voor Nextcloud/ownCloud.",
      reentry:
        "De URL, het servertype of de gebruikersnaam is gewijzigd. Vul het wachtwoord opnieuw in: bekup stuurt een opgeslagen wachtwoord nooit naar een nieuw eindpunt.",
      tlsLegend: "TLS en time-outs",
      tlsLead:
        "Een zelfondertekend certificaat heeft geen CA-bundel nodig: test de bestemming en pin dan het certificaat dat getoond wordt.",
      caBundle: "Eigen CA-bundel (PEM, optioneel)",
      caBundleHint:
        "Vervangt de systeemroots voor deze server (een NAS of privé-CA). Niet gebruikt zolang een certificaat vastgepind is.",
      timeout: "Time-out per verzoek (seconden, optioneel)",
      timeoutHint:
        "Eén metadataverzoek; 5 tot 600, standaard 60. Uploads worden er niet door beperkt.",
    },
    errors: {
      url: "Vul de https://-URL in.",
      urlScheme: "Vul een https://-URL in (http alleen in dev-modus).",
      username: "Vul de gebruikersnaam in.",
      usernameColon: "De gebruikersnaam mag geen „:” bevatten.",
      reentry:
        "De URL, het servertype of de gebruikersnaam is gewijzigd: voer het wachtwoord opnieuw in. Een opgeslagen wachtwoord wordt nooit naar een nieuw eindpunt gestuurd.",
      caBundle: "Plak de CA-bundel als PEM.",
      timeout: "Vul een geheel aantal seconden in tussen 5 en 600.",
      path: "Gebruik een map zonder „..”.",
    },
    facts: {
      url: "URL",
      flavor: "Servertype",
      username: "Gebruikersnaam",
      path: "Map",
      pathBase: "(de URL zelf)",
      pathFiles: "(de bestanden van de gebruiker)",
      password: "Wachtwoord",
      timeout: "Time-out per verzoek",
      timeoutValue: (n: number | null | undefined) =>
        n == null ? "60 s (standaard)" : `${String(n)} s`,
      caBundle: "Eigen CA-bundel",
      copyCaBundle: "CA-bundel kopiëren",
    },
    tls: {
      title: "TLS",
      lead: "Hoe bekup het certificaat van de server controleert voordat het wachtwoord wordt verstuurd.",
      trust: "Gecontroleerd tegen",
      pinned: "Vastgepind certificaat",
      notPinnedHint:
        "Wil je een zelfondertekend certificaat gebruiken, voer dan de test hierboven uit en pin het certificaat dat getoond wordt.",
      removePin: "Pin verwijderen",
      removeTitle: "Certificaatpin verwijderen?",
      removeLead:
        "bekup controleert de server dan weer tegen de systeemroots, of tegen de CA-bundel van de bestemming als die is ingesteld. Een zelfondertekend certificaat faalt vanaf dan tot je het opnieuw pint. Het verwijderen van de pin komt in de auditlog en geeft een beveiligingsmelding.",
      removed: "Certificaatpin verwijderd.",
    },
    cert: {
      spki: "Publieke sleutel (SPKI SHA-256)",
      spkiShort: "SPKI SHA-256",
      fingerprint: "Certificaat SHA-256",
      valid: "Geldig",
      validRange: (from: string, to: string) => `${from} → ${to}`,
      pinnedAt: "Vastgepind",
      selfSigned: "Zelfondertekend",
      caIssued: "Uitgegeven door een CA",
      expired: "Verlopen",
      notYet: "Nog niet geldig",
      copySpki: "SPKI SHA-256 (hex) kopiëren",
      copyFingerprint: "Certificaat SHA-256 kopiëren",
    },
    test: {
      heading: "TLS en server",
      certificate: "Certificaat",
      noCertificate: "Geen gezien (onbereikbaar of gewone http).",
      trust: "Controle",
      trusted: "Vertrouwd",
      untrusted: "Niet vertrouwd",
      pinMatches: "Komt overeen met de pin",
      pinMismatch: "Komt niet overeen met de pin",
      server: "Server",
      serverUnknown: "Generieke WebDAV",
      uploadMode: "Uploads",
      overwrite: "Bescherming tegen overschrijven",
      notRun: "Niet uitgevoerd",
      suggestFlavor: (server: string) =>
        `Dit lijkt ${server}: gebruik het servertype Nextcloud / ownCloud, zodat grote dumps in stukken worden geüpload.`,
      suggestGeneric:
        "De server antwoordde niet zoals Nextcloud of ownCloud (geen status.php). Is het een gewone WebDAV-server, gebruik dan het servertype Generieke WebDAV.",
      ignoredTitle: "Deze server zou bestaande back-ups vervangen",
      ignoredBody:
        "bekup vroeg de server een bestand over een bestaand bestand te verplaatsen met „Overwrite: F”, wat een WebDAV-server moet weigeren. Deze server negeerde dat en verving het bestand, dus een herhaalde of gelijktijdige upload kan ongemerkt een opgeslagen back-up overschrijven. bekup weigert ernaar te schrijven. Gebruik een andere server, of herstel de WebDAV-module van de server, en test opnieuw.",
      untrustedHint:
        "Het certificaat kwam niet door de controle. Is het het eigen zelfondertekende certificaat van de server, vergelijk dan de vingerafdruk en pin het.",
      mismatchHint:
        "De server toont een andere sleutel dan de vastgepinde. Pin hem alleen als het certificaat bewust is vervangen: een onverwachte wijziging kan betekenen dat iemand de verbinding onderschept.",
      pin: "Dit certificaat vastpinnen",
      pinnedNow: "Certificaat vastgepind. Test opnieuw om de verbinding met de pin te controleren.",
    },
    pin: {
      title: "Dit certificaat vastpinnen",
      lead: "Na het vastpinnen accepteert bekup voor de server alleen de sleutel van dit certificaat: geen systeemroots, geen CA-bundel. Vergelijk eerst de vingerafdruk met de server via een ander kanaal.",
      compareHint: "Op de server, bijvoorbeeld:",
      command:
        "openssl x509 -in cert.pem -pubkey -noout | openssl pkey -pubin -outform der | openssl dgst -sha256",
      browserHint:
        "Browsers en openssl x509 -fingerprint -sha256 tonen in plaats daarvan de SHA-256 van het certificaat.",
      renewal:
        "Wordt het certificaat vernieuwd met een nieuwe sleutel (Let's Encrypt doet dat standaard), dan mislukken back-ups tot je het nieuwe vastpint.",
      compared:
        "Ik heb de vingerafdruk via een ander kanaal met de server vergeleken en hij komt overeen.",
      confirm: "Certificaat vastpinnen",
      cancel: "Annuleren",
      pinned: "Certificaat vastgepind.",
      conflict:
        "De server toont nu een ander certificaat dan het certificaat hier. Er is niets vastgepind: test de bestemming opnieuw en vergelijk de nieuwe vingerafdruk.",
    },
  },
};

const M = defineMessages({ en, nl });

export const FLAVOR_LABEL: Record<WebdavFlavor, string> = M.flavor;

export const FLAVOR_DESCRIPTION: Record<WebdavFlavor, string> = M.flavorDescription;

export const URL_HELP: Record<WebdavFlavor, { description: string; placeholder: string }> =
  M.urlHelp;

export const PATH_HELP: Record<WebdavFlavor, string> = M.pathHelp;

export const TRUST_LABEL: Record<WebdavTrust, string> = M.trust;

export const TRUST_DESCRIPTION: Record<WebdavTrust, string> = M.trustDescription;

export const SERVER_LABEL: Record<WebdavServerKind, string> = M.server;

export const UPLOAD_LABEL: Record<WebdavUploadMode, string> = M.upload;

export const OVERWRITE_LABEL: Record<WebdavOverwriteProbe, string> = M.overwrite;

export const WT = M.wt;
