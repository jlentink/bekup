/** Strings of the browser setup (D-217). */
export const T = {
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
  slipGone: "The passphrase was revealed already and cannot be shown again.",
  // Results
  doneTitle: "Setup finished",
  failedTitle: "Setup stopped",
  reportHeading: "Report",
  zip: "Download configuration (zip)",
  zipHint:
    "compose.yaml, .env, bekup.toml, runner.toml and the state file. No keys, passwords or certificates: those stay on the server.",
  finish: "Finish and stop setup",
  finished: "Setup has stopped. You can close this tab.",
  // Errors
  lost: "The setup server is gone (finished, idle or stopped). Start it again on the server if you need it.",
  retry: "Try again",
} as const;
