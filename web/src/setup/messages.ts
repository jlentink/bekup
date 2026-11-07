import { defineMessages } from "@/lib/i18n";

/**
 * Strings of the browser setup's own chrome (D-217), in English and Dutch
 * (D-235). The wizard's questions come from the server and stay English.
 */
export const T = defineMessages({
  en: {
    product: "bekup",
    title: "Setup",
    ledgerHeading: "Answered",
    ledgerEmpty: "Your answers appear here as you go.",
    secretShown: "entered",
    sensitiveShown: "stored offline",
    // Sign in
    signInTitle: "Open the setup",
    signInLead:
      "The terminal that started this setup shows a one-time access code and the fingerprint of this page's certificate.",
    fingerprintCheck:
      "Before you enter the code, open the certificate in your browser's address bar and compare its SHA-256 fingerprint with the terminal. Continue only if they match.",
    codeLabel: "Access code",
    codeHint: "26 characters; spaces and dashes are ignored. It works once.",
    signIn: "Open setup",
    signingIn: "Opening…",
    // Steps
    continue: "Continue",
    back: "Back",
    yes: "Yes",
    no: "No",
    sending: "Sending…",
    secretHint: "Never shown again and never stored in the browser. Leave it empty to go back.",
    working: "Setup is working…",
    workingLead:
      "Writing files, checking the deployment or talking to the database. This page updates by itself.",
    // Show once
    slipTitle: "Shown once",
    slipLead:
      "This passphrase is shown exactly once. Store it offline with the CA (a password manager or paper in a safe).",
    slipReveal: "Reveal passphrase",
    slipRevealing: "Revealing…",
    slipCopy: "Copy",
    slipCopied: "Copied",
    slipConfirm: "I have stored it offline",
    slipGone:
      "The passphrase was revealed already on this screen. If you could not store it, go Back and continue again: the next screen shows it once more.",
    // Results
    doneTitle: "Setup finished",
    failedTitle: "Setup stopped",
    reportHeading: "Report",
    zip: "Download configuration (zip)",
    zipHint:
      "compose.yaml, the variables bekup setup manages from .env, bekup.toml, runner.toml, external-api.toml (when enabled), the state file and compose.override.yaml while it is exactly what setup wrote. No keys, passwords or certificates: those stay on the server.",
    zipNotesHeading: "Left out of the zip",
    finish: "Finish and stop setup",
    finished: "Setup has stopped. You can close this tab.",
    // Errors
    lost: "The setup server is gone (finished, idle or stopped). Start it again on the server if you need it.",
    retry: "Try again",
    requestFailed: (status: number) => `request failed (${status})`,
  },
  nl: {
    product: "bekup",
    title: "Installatie",
    ledgerHeading: "Beantwoord",
    ledgerEmpty: "Je antwoorden verschijnen hier terwijl je verdergaat.",
    secretShown: "ingevoerd",
    sensitiveShown: "offline bewaard",
    // Sign in
    signInTitle: "Installatie openen",
    signInLead:
      "De terminal die deze installatie heeft gestart, toont een eenmalige toegangscode en de vingerafdruk van het certificaat van deze pagina.",
    fingerprintCheck:
      "Open voordat je de code invoert het certificaat via de adresbalk van je browser en vergelijk de SHA-256-vingerafdruk met de terminal. Ga alleen verder als ze overeenkomen.",
    codeLabel: "Toegangscode",
    codeHint: "26 tekens; spaties en streepjes worden genegeerd. De code werkt één keer.",
    signIn: "Installatie openen",
    signingIn: "Openen…",
    // Steps
    continue: "Doorgaan",
    back: "Terug",
    yes: "Ja",
    no: "Nee",
    sending: "Versturen…",
    secretHint:
      "Wordt nooit meer getoond en nooit in de browser opgeslagen. Laat het leeg om terug te gaan.",
    working: "De installatie is bezig…",
    workingLead:
      "Bestanden schrijven, de installatie controleren of met de database praten. Deze pagina werkt zichzelf bij.",
    // Show once
    slipTitle: "Eenmalig getoond",
    slipLead:
      "Deze wachtwoordzin wordt precies één keer getoond. Bewaar hem offline bij de CA (een wachtwoordmanager of op papier in een kluis).",
    slipReveal: "Wachtwoordzin tonen",
    slipRevealing: "Tonen…",
    slipCopy: "Kopiëren",
    slipCopied: "Gekopieerd",
    slipConfirm: "Ik heb hem offline bewaard",
    slipGone:
      "De wachtwoordzin is op dit scherm al getoond. Kon je hem niet bewaren, ga dan Terug en daarna weer verder: het volgende scherm toont hem nog één keer.",
    // Results
    doneTitle: "Installatie voltooid",
    failedTitle: "Installatie gestopt",
    reportHeading: "Verslag",
    zip: "Configuratie downloaden (zip)",
    zipHint:
      "compose.yaml, de variabelen uit .env die bekup setup beheert, bekup.toml, runner.toml, external-api.toml (als die is ingeschakeld), het statusbestand en compose.override.yaml zolang dat precies is wat setup schreef. Geen sleutels, wachtwoorden of certificaten: die blijven op de server.",
    zipNotesHeading: "Niet in de zip",
    finish: "Afronden en installatie stoppen",
    finished: "De installatie is gestopt. Je kunt dit tabblad sluiten.",
    // Errors
    lost: "De installatieserver is weg (klaar, inactief of gestopt). Start hem opnieuw op de server als je hem nodig hebt.",
    retry: "Opnieuw proberen",
    requestFailed: (status: number) => `verzoek mislukt (${status})`,
  },
});
