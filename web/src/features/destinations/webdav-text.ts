import type {
  WebdavFlavor,
  WebdavOverwriteProbe,
  WebdavServerKind,
  WebdavTrust,
  WebdavUploadMode,
} from "@/graphql/gen/graphql";

/**
 * Strings of the WebDAV destination UI (D-222): form, detail page, test
 * details and the certificate pin flow.
 */

export const FLAVOR_LABEL: Record<WebdavFlavor, string> = {
  GENERIC: "Generic WebDAV",
  NEXTCLOUD: "Nextcloud / ownCloud",
};

export const FLAVOR_DESCRIPTION: Record<WebdavFlavor, string> = {
  GENERIC:
    "Plain WebDAV (NAS, Apache, nginx, rclone, Caddy, Hetzner Storage Box): every file is one streaming PUT.",
  NEXTCLOUD:
    "Nextcloud or ownCloud, TransIP STACK too: large files go up in 64 MiB chunks, so PHP and proxy upload limits never cut off a multi-GB dump.",
};

export const URL_HELP: Record<WebdavFlavor, { description: string; placeholder: string }> = {
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
};

export const PATH_HELP: Record<WebdavFlavor, string> = {
  GENERIC: "Optional folder below the URL; created if missing. Empty: the URL itself.",
  NEXTCLOUD: "Optional folder below the user's files, e.g. Backups/bekup; created if missing.",
};

export const TRUST_LABEL: Record<WebdavTrust, string> = {
  SYSTEM: "System roots",
  CA_BUNDLE: "Own CA bundle",
  PINNED: "Pinned certificate",
  PLAIN_HTTP: "Plain HTTP (dev mode)",
};

export const TRUST_DESCRIPTION: Record<WebdavTrust, string> = {
  SYSTEM: "The certificate must chain to a public root CA the server trusts and name the host.",
  CA_BUNDLE: "The certificate must chain to the destination's own CA bundle (system roots unused).",
  PINNED:
    "Only the pinned certificate's key is accepted: no system roots, no CA bundle. A renewal with a new key needs a new pin.",
  PLAIN_HTTP:
    "No TLS at all: the password and the encrypted backups travel unprotected. Accepted in dev mode only.",
};

export const SERVER_LABEL: Record<WebdavServerKind, string> = {
  NEXTCLOUD: "Nextcloud",
  OWNCLOUD: "ownCloud",
};

export const UPLOAD_LABEL: Record<WebdavUploadMode, string> = {
  SINGLE: "One streaming PUT",
  CHUNKED: "Chunked upload (Nextcloud/ownCloud API)",
};

export const OVERWRITE_LABEL: Record<WebdavOverwriteProbe, string> = {
  REFUSED: "Refused: existing files are never replaced",
  IGNORED: "Ignored: the server replaces existing files",
};

export const WT = {
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
} as const;
