import { defineMessages, plural, type Messages } from "@/lib/i18n";

/**
 * Strings of the user administration and owner assignment pages (task
 * 6.8), in English and Dutch (D-235). Kept next to the feature; same rules
 * as lib/messages.ts: short, calm, no internal details.
 */
const en = {
  eyebrow: "Administration",
  title: "Users",
  lead: "Accounts, roles and access. Every change asks you to confirm your identity and is written to the audit log.",
  create: "New user",
  twoFactorPolicy: "2FA policy",
  searchLabel: "Search users",
  searchPlaceholder: "Name or e-mail",
  roleFilter: "Role",
  statusFilter: "Status",
  anyRole: "Any role",
  anyStatus: "Any status",
  active: "Active",
  disabledFilter: "Disabled",
  includeDeleted: "Show deleted",
  empty: "No accounts match these filters.",
  previous: "Previous",
  next: "Next",
  page: (n: number) => `Page ${String(n)}`,
  columns: {
    user: "User",
    role: "Role",
    status: "Status",
    twoFactor: "2FA",
    lastSeen: "Last seen",
    created: "Created",
  },
  status: {
    active: "Active",
    disabled: "Disabled",
    deleted: "Deleted",
    locked: "Locked",
    temporary: "Temporary password",
    temporaryExpired: "Temporary password expired",
    unverified: "E-mail unverified",
  },
  twoFactor: {
    enrolled: "Enrolled",
    grace: (when: string) => `Due ${when}`,
    overdue: "Enrollment overdue",
    none: "None",
    enrolledHint: "The account has an authenticator app or a passkey.",
    enrolledRequiredHint:
      "The policy requires a second factor and the account has an authenticator app or a passkey.",
    graceHint: "The policy requires a second factor; the account has none yet.",
    overdueHint: "The grace period is over: the account can only enroll a second factor.",
    noneHint:
      "No authenticator app or passkey (recovery codes alone do not count). The 2FA policy does not require one for this role.",
  },
  lastSeen: {
    never: "Never",
    hint: "The newest activity of a fully signed-in session.",
  },
  youTag: "You",
  // Create
  createTitle: "Create an account",
  createLead:
    "bekup generates a temporary password. You never choose another person's password: hand the temporary one over through a trusted channel.",
  email: "E-mail",
  displayName: "Name",
  role: "Role",
  roleHints: {
    ADMIN: "Full control, including users, keys and security settings.",
    MAINTAINER: "Manages connections, destinations and every job. No downloads, no user admin.",
    USER: "Sees and runs only the jobs they own (owner grants).",
  },
  createSubmit: "Create account",
  creating: "Creating…",
  emailInvalid: "Enter a valid e-mail address.",
  nameRequired: "Enter a name.",
  nameTooLong: "Keep the name under 200 characters.",
  created: "Account created.",
  // Temporary password
  tempEyebrow: "One-time temporary password",
  tempTitleCreated: (email: string) => `${email} can now sign in`,
  tempTitleReset: (email: string) => `New temporary password for ${email}`,
  tempLead:
    "Give this password to the person through a trusted channel. They must choose a new one at their first sign-in.",
  tempWarning:
    "This password will not be shown again. bekup does not store it in readable form; if it is lost, reset the password again.",
  tempExpires: (when: string, rel: string) => `Expires ${when} (${rel}).`,
  tempPasswordLabel: "Temporary password",
  tempCopied: "Temporary password copied.",
  tempAcknowledge: "I have handed over or stored the password",
  tempDone: "Done",
  // Detail
  back: "All users",
  notFound: "This account does not exist.",
  deletedBanner: (when: string) =>
    `This account was deleted ${when}. It is kept for the audit log.`,
  profileHeading: "Profile",
  profileLead: "Name and role. A role change signs the person out everywhere.",
  save: "Save changes",
  saved: "Account updated.",
  noChanges: "Nothing changed.",
  roleChangeTitle: "Change the role?",
  roleChange: (from: string, to: string) =>
    `The role changes from ${from} to ${to}. All sessions of this account are revoked: the person has to sign in again.`,
  roleChangeConfirm: "Change role",
  roleOwnedJobsNote:
    "Owner grants only take effect for the User role. Jobs this account owns stay listed but give no extra rights to other roles.",
  selfRoleNote: "You cannot change your own role here; another admin has to do that.",
  accessHeading: "Access",
  accessLead: "Sign-in state, lockouts and passwords.",
  disable: "Disable account",
  enable: "Enable account",
  disableTitle: "Disable this account?",
  disableLead: "The person cannot sign in and all their sessions are revoked immediately.",
  disabled: "Account disabled. Its sessions were revoked.",
  enabled: "Account enabled.",
  unlock: "Clear lockout",
  unlocked: "Lockout cleared.",
  lockedUntil: (when: string) => `Locked after failed sign-ins until ${when}.`,
  temporaryPending: (when: string) => `Temporary password not changed yet; it expires ${when}.`,
  temporaryExpired: "The temporary password expired. Reset the password to issue a new one.",
  resetPassword: "Reset password",
  resetPasswordTitle: "Reset the password?",
  resetPasswordLead:
    "bekup replaces the password with a new temporary one (valid for 72 hours), clears the lockout and revokes all sessions of this account.",
  selfResetPassword: "Change your own password in your profile.",
  twoFactorHeading: "Second factors",
  twoFactorLead: "For a person who lost their authenticator or passkeys.",
  resetFactors: "Reset second factors",
  resetFactorsTitle: "Reset all second factors?",
  resetFactorsLead:
    "This removes the account's TOTP app, every passkey and all recovery codes, and revokes its sessions. The person must enroll a new second factor; if the 2FA policy requires one, a new grace period starts now. Only do this after verifying the person's identity out of band.",
  resetFactorsDone: (totp: boolean, passkeys: number, codes: number) =>
    `Second factors reset: ${totp ? "TOTP removed, " : ""}${plural(passkeys, { one: "1 passkey", other: `${String(passkeys)} passkeys` })} and ${plural(codes, { one: "1 recovery code", other: `${String(codes)} recovery codes` })} removed.`,
  selfResetFactors:
    "You cannot reset your own second factors. Manage them in your profile, or ask another admin.",
  dangerHeading: "Delete",
  dangerLead:
    "Deleting disables the account, removes its password and revokes its sessions. The record stays for the audit log; the e-mail address can be reused.",
  delete: "Delete account",
  deleteTitle: "Delete this account?",
  deleteLead: (email: string) =>
    `${email} will no longer be able to sign in. This cannot be undone from the UI.`,
  deleteConfirmLabel: (email: string) => `Type ${email} to confirm`,
  deleted: "Account deleted.",
  selfDelete: "You cannot delete your own account.",
  lastAdmin:
    "This is the last active admin. Promote or enable another admin first, so the instance always keeps one.",
  // Sessions
  sessionsHeading: "Sessions",
  sessionsLead: "Where this account is signed in, most recently used first.",
  noSessions: "No active sessions.",
  revokeAll: "Revoke all sessions",
  revokeAllTitle: "Revoke every session?",
  revokeAllLead: "The person is signed out on every device and has to sign in again.",
  revokedCount: (n: number) =>
    plural(n, { one: "1 session revoked.", other: `${String(n)} sessions revoked.` }),
  lastActive: (rel: string) => `Last active ${rel}`,
  neverActive: "No active session",
  // Owned jobs
  jobsHeading: "Owned jobs",
  jobsLead:
    "Backup owners see, run and restore their own jobs (and download their backups). Adding or removing a job is audited and alerts the admins.",
  noJobs: "This account owns no jobs.",
  assign: "Assign jobs",
  assignTitle: "Assign jobs",
  assignLead: (name: string) => `Choose jobs ${name} should own.`,
  assignSearch: "Search jobs",
  assignSubmit: (n: number) =>
    plural(n, { one: "Assign 1 job", other: `Assign ${String(n)} jobs` }),
  assignNone: "No more jobs to assign.",
  assigned: (n: number) =>
    plural(n, { one: "1 job assigned.", other: `${String(n)} jobs assigned.` }),
  remove: "Remove",
  removeLabel: (job: string) => `Remove ownership of ${job}`,
  removeTitle: "Remove this ownership?",
  removeLead: (job: string) => `The account loses access to ${job}.`,
  removed: "Ownership removed.",
  ownerRoleOnly:
    "Owner grants give rights to accounts with the User role only (D-067). Admins and maintainers already reach jobs through their role.",
  jobDisabled: "disabled",
  loadMoreJobs: "Show more",
  assignTruncated: "More jobs match: refine the search to find others.",
  // Picker
  pickerLabel: "Backup owners",
  pickerSearch: "Search accounts",
  pickerEmpty: "No accounts match.",
  pickerSelected: "Selected",
  pickerRoleOnly: "Only accounts with the User role can own jobs.",
  pickerReason: {
    role: "Admins and maintainers already have access through their role",
    disabled: "Account disabled",
    deleted: "Account deleted",
  },
  pickerForbidden:
    "Only admins and maintainers can browse accounts. Ask one of them to assign owners.",
  pickerRemove: (name: string) => `Remove ${name}`,
};

const nl: Messages<typeof en> = {
  eyebrow: "Beheer",
  title: "Gebruikers",
  lead: "Accounts, rollen en toegang. Bij elke wijziging bevestig je je identiteit en elke wijziging komt in de auditlog.",
  create: "Nieuwe gebruiker",
  twoFactorPolicy: "2FA-beleid",
  searchLabel: "Gebruikers zoeken",
  searchPlaceholder: "Naam of e-mail",
  roleFilter: "Rol",
  statusFilter: "Status",
  anyRole: "Elke rol",
  anyStatus: "Elke status",
  active: "Actief",
  disabledFilter: "Uitgeschakeld",
  includeDeleted: "Verwijderde tonen",
  empty: "Geen accounts die aan deze filters voldoen.",
  previous: "Vorige",
  next: "Volgende",
  page: (n: number) => `Pagina ${String(n)}`,
  columns: {
    user: "Gebruiker",
    role: "Rol",
    status: "Status",
    twoFactor: "2FA",
    lastSeen: "Laatst gezien",
    created: "Aangemaakt",
  },
  status: {
    active: "Actief",
    disabled: "Uitgeschakeld",
    deleted: "Verwijderd",
    locked: "Geblokkeerd",
    temporary: "Tijdelijk wachtwoord",
    temporaryExpired: "Tijdelijk wachtwoord verlopen",
    unverified: "E-mail niet geverifieerd",
  },
  twoFactor: {
    enrolled: "Ingesteld",
    grace: (when: string) => `Uiterlijk ${when}`,
    overdue: "Instellen te laat",
    none: "Geen",
    enrolledHint: "Het account heeft een authenticator-app of een passkey.",
    enrolledRequiredHint:
      "Het beleid vereist een tweede factor en het account heeft een authenticator-app of een passkey.",
    graceHint: "Het beleid vereist een tweede factor; het account heeft er nog geen.",
    overdueHint:
      "De overgangsperiode is voorbij: het account kan alleen nog een tweede factor instellen.",
    noneHint:
      "Geen authenticator-app of passkey (alleen herstelcodes tellen niet). Het 2FA-beleid vereist er geen voor deze rol.",
  },
  lastSeen: {
    never: "Nooit",
    hint: "De nieuwste activiteit van een volledig ingelogde sessie.",
  },
  youTag: "Jij",
  // Create
  createTitle: "Account aanmaken",
  createLead:
    "bekup maakt een tijdelijk wachtwoord. Je kiest nooit het wachtwoord van iemand anders: geef het tijdelijke wachtwoord door via een betrouwbaar kanaal.",
  email: "E-mail",
  displayName: "Naam",
  role: "Rol",
  roleHints: {
    ADMIN: "Volledige controle, inclusief gebruikers, sleutels en beveiligingsinstellingen.",
    MAINTAINER:
      "Beheert verbindingen, bestemmingen en elke taak. Geen downloads, geen gebruikersbeheer.",
    USER: "Ziet en start alleen de taken waarvan hij of zij eigenaar is.",
  },
  createSubmit: "Account aanmaken",
  creating: "Bezig met aanmaken…",
  emailInvalid: "Vul een geldig e-mailadres in.",
  nameRequired: "Vul een naam in.",
  nameTooLong: "Houd de naam korter dan 200 tekens.",
  created: "Account aangemaakt.",
  // Temporary password
  tempEyebrow: "Eenmalig tijdelijk wachtwoord",
  tempTitleCreated: (email: string) => `${email} kan nu inloggen`,
  tempTitleReset: (email: string) => `Nieuw tijdelijk wachtwoord voor ${email}`,
  tempLead:
    "Geef dit wachtwoord via een betrouwbaar kanaal aan de persoon. Bij de eerste keer inloggen moet die een nieuw wachtwoord kiezen.",
  tempWarning:
    "Dit wachtwoord wordt niet meer getoond. bekup bewaart het niet in leesbare vorm; ben je het kwijt, reset het wachtwoord dan opnieuw.",
  tempExpires: (when: string, rel: string) => `Verloopt ${when} (${rel}).`,
  tempPasswordLabel: "Tijdelijk wachtwoord",
  tempCopied: "Tijdelijk wachtwoord gekopieerd.",
  tempAcknowledge: "Ik heb het wachtwoord doorgegeven of opgeslagen",
  tempDone: "Klaar",
  // Detail
  back: "Alle gebruikers",
  notFound: "Dit account bestaat niet.",
  deletedBanner: (when: string) =>
    `Dit account is verwijderd ${when}. Het blijft bewaard voor de auditlog.`,
  profileHeading: "Profiel",
  profileLead: "Naam en rol. Na een rolwijziging wordt de persoon overal uitgelogd.",
  save: "Wijzigingen opslaan",
  saved: "Account bijgewerkt.",
  noChanges: "Er is niets gewijzigd.",
  roleChangeTitle: "De rol wijzigen?",
  roleChange: (from: string, to: string) =>
    `De rol verandert van ${from} naar ${to}. Alle sessies van dit account worden ingetrokken: de persoon moet opnieuw inloggen.`,
  roleChangeConfirm: "Rol wijzigen",
  roleOwnedJobsNote:
    "Eigenaarschap geeft alleen rechten bij de rol Gebruiker. Taken van dit account blijven in de lijst staan, maar geven andere rollen geen extra rechten.",
  selfRoleNote: "Je kunt je eigen rol hier niet wijzigen; een andere beheerder moet dat doen.",
  accessHeading: "Toegang",
  accessLead: "Inlogstatus, blokkeringen en wachtwoorden.",
  disable: "Account uitschakelen",
  enable: "Account inschakelen",
  disableTitle: "Dit account uitschakelen?",
  disableLead: "De persoon kan niet meer inloggen en alle sessies worden meteen ingetrokken.",
  disabled: "Account uitgeschakeld. De sessies zijn ingetrokken.",
  enabled: "Account ingeschakeld.",
  unlock: "Blokkering opheffen",
  unlocked: "Blokkering opgeheven.",
  lockedUntil: (when: string) => `Geblokkeerd na mislukte inlogpogingen tot ${when}.`,
  temporaryPending: (when: string) =>
    `Tijdelijk wachtwoord nog niet gewijzigd; het verloopt ${when}.`,
  temporaryExpired:
    "Het tijdelijke wachtwoord is verlopen. Reset het wachtwoord om een nieuw te maken.",
  resetPassword: "Wachtwoord resetten",
  resetPasswordTitle: "Het wachtwoord resetten?",
  resetPasswordLead:
    "bekup vervangt het wachtwoord door een nieuw tijdelijk wachtwoord (72 uur geldig), heft de blokkering op en trekt alle sessies van dit account in.",
  selfResetPassword: "Wijzig je eigen wachtwoord in je profiel.",
  twoFactorHeading: "Tweede factoren",
  twoFactorLead: "Voor iemand die de authenticator-app of passkeys kwijt is.",
  resetFactors: "Tweede factoren resetten",
  resetFactorsTitle: "Alle tweede factoren resetten?",
  resetFactorsLead:
    "Dit verwijdert de TOTP-app van het account, elke passkey en alle herstelcodes, en trekt de sessies in. De persoon moet een nieuwe tweede factor instellen; als het 2FA-beleid er een vereist, begint nu een nieuwe overgangsperiode. Doe dit alleen nadat je de identiteit van de persoon langs een andere weg hebt gecontroleerd.",
  resetFactorsDone: (totp: boolean, passkeys: number, codes: number) =>
    `Tweede factoren gereset: ${totp ? "TOTP verwijderd, " : ""}${plural(passkeys, { one: "1 passkey", other: `${String(passkeys)} passkeys` })} en ${plural(codes, { one: "1 herstelcode", other: `${String(codes)} herstelcodes` })} verwijderd.`,
  selfResetFactors:
    "Je kunt je eigen tweede factoren niet resetten. Beheer ze in je profiel of vraag een andere beheerder.",
  dangerHeading: "Verwijderen",
  dangerLead:
    "Verwijderen schakelt het account uit, haalt het wachtwoord weg en trekt de sessies in. Het record blijft bewaard voor de auditlog; het e-mailadres kan opnieuw worden gebruikt.",
  delete: "Account verwijderen",
  deleteTitle: "Dit account verwijderen?",
  deleteLead: (email: string) =>
    `${email} kan niet meer inloggen. Dit kan niet ongedaan worden gemaakt in de UI.`,
  deleteConfirmLabel: (email: string) => `Typ ${email} om te bevestigen`,
  deleted: "Account verwijderd.",
  selfDelete: "Je kunt je eigen account niet verwijderen.",
  lastAdmin:
    "Dit is de laatste actieve beheerder. Maak eerst een andere beheerder aan of schakel er een in, zodat de instantie er altijd een houdt.",
  // Sessions
  sessionsHeading: "Sessies",
  sessionsLead: "Waar dit account is ingelogd, meest recent gebruikt eerst.",
  noSessions: "Geen actieve sessies.",
  revokeAll: "Alle sessies intrekken",
  revokeAllTitle: "Elke sessie intrekken?",
  revokeAllLead: "De persoon wordt op elk apparaat uitgelogd en moet opnieuw inloggen.",
  revokedCount: (n: number) =>
    plural(n, { one: "1 sessie ingetrokken.", other: `${String(n)} sessies ingetrokken.` }),
  lastActive: (rel: string) => `Laatst actief ${rel}`,
  neverActive: "Geen actieve sessie",
  // Owned jobs
  jobsHeading: "Taken als eigenaar",
  jobsLead:
    "Eigenaren zien, starten en herstellen hun eigen taken (en downloaden hun back-ups). Het toevoegen of verwijderen van een taak komt in de auditlog en de beheerders krijgen een melding.",
  noJobs: "Dit account is van geen enkele taak eigenaar.",
  assign: "Taken toewijzen",
  assignTitle: "Taken toewijzen",
  assignLead: (name: string) => `Kies de taken waarvan ${name} eigenaar moet worden.`,
  assignSearch: "Taken zoeken",
  assignSubmit: (n: number) =>
    plural(n, { one: "1 taak toewijzen", other: `${String(n)} taken toewijzen` }),
  assignNone: "Geen taken meer om toe te wijzen.",
  assigned: (n: number) =>
    plural(n, { one: "1 taak toegewezen.", other: `${String(n)} taken toegewezen.` }),
  remove: "Verwijderen",
  removeLabel: (job: string) => `Eigenaarschap van ${job} verwijderen`,
  removeTitle: "Dit eigenaarschap verwijderen?",
  removeLead: (job: string) => `Het account verliest de toegang tot ${job}.`,
  removed: "Eigenaarschap verwijderd.",
  ownerRoleOnly:
    "Eigenaarschap geeft alleen rechten aan accounts met de rol Gebruiker (D-067). Beheerders en onderhouders bereiken taken al via hun rol.",
  jobDisabled: "uitgeschakeld",
  loadMoreJobs: "Meer tonen",
  assignTruncated:
    "Er zijn meer overeenkomende taken: verfijn de zoekopdracht om andere te vinden.",
  // Picker
  pickerLabel: "Eigenaren",
  pickerSearch: "Accounts zoeken",
  pickerEmpty: "Geen overeenkomende accounts.",
  pickerSelected: "Geselecteerd",
  pickerRoleOnly: "Alleen accounts met de rol Gebruiker kunnen eigenaar van taken zijn.",
  pickerReason: {
    role: "Beheerders en onderhouders hebben al toegang via hun rol",
    disabled: "Account uitgeschakeld",
    deleted: "Account verwijderd",
  },
  pickerForbidden:
    "Alleen beheerders en onderhouders kunnen accounts doorbladeren. Vraag een van hen om eigenaren toe te wijzen.",
  pickerRemove: (name: string) => `${name} verwijderen`,
};

export const usersMessages = defineMessages({ en, nl });
