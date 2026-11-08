# Dutch glossary (D-235, D-237)

Used by the web UI and by the server texts (`texts.go` catalogs: inbox,
e-mails, summaries).

Every Dutch text in the UI uses these terms, so the same thing always has
the same name. Add a row when you introduce a new concept; never use a
synonym for an existing one.

## Style

- Address the user with **je/jij/jouw** (informal), never "u".
- Short, calm sentences, like the English texts. No exclamation marks.
- Sentence case for headings and buttons ("Back-up starten", not
  "Back-Up Starten").
- Buttons and menu items are infinitives ("Opslaan", "Verwijderen",
  "Herstel starten"); confirmations end in a question mark
  ("Taak verwijderen?"); toasts are short past-tense statements
  ("Taak verwijderd.").
- Dutch quotes „…” around names (the English texts use “…”).
- Keep untranslated: product name `bekup` (lower case), code, CLI commands,
  config keys, file names, protocol and product names (S3, SFTP, WebDAV,
  TLS, SSH, OIDC, TOTP, SHA-256, PostgreSQL, MySQL, MariaDB, Prometheus,
  Object Lock, Nextcloud, ...), units (KiB, MiB, GiB), placeholders like
  `${name}`.
- Numbers, dates and sizes are never formatted in the message: they come
  formatted from `lib/format` (which follows the page's locale).
- Plurals with `plural(n, { one, other })` from `lib/i18n`.

- Input prompts: "Vul … in" (never "Voer … in"; "Voer … uit" means run).
  Limits: "hooguit". Create: "aanmaken" / "aangemaakt".
- Apostrophes: straight (`risico's`), like the English texts.
- Durations keep the compact units of `lib/format` (`2h 05m`, `42s`) in
  both languages.
- `n === 1 ? … : …` belongs in `plural()`; a ternary is fine only when it
  is not a grammatical plural (e.g. "Add owner" vs "Add 3 owners").
- Texts outside JavaScript (`<title>`, `<noscript>` in `index.html`) stay
  English.

## Terms

| English                         | Nederlands                                                     |
| ------------------------------- | -------------------------------------------------------------- |
| backup (noun)                   | back-up (mv. back-ups)                                         |
| back up (verb)                  | een back-up maken                                              |
| backup job, job                 | back-uptaak, taak                                              |
| run (of a job)                  | run (mv. runs)                                                 |
| run now                         | nu uitvoeren                                                   |
| start early                     | eerder starten                                                 |
| scheduled / manual run          | geplande / handmatige run                                      |
| schedule                        | planning                                                       |
| next run                        | volgende run                                                   |
| queued / running                | in de wachtrij / bezig                                         |
| succeeded / success             | geslaagd                                                       |
| failed / failure                | mislukt / fout                                                 |
| partial                         | gedeeltelijk                                                   |
| canceled                        | geannuleerd                                                    |
| skipped                         | overgeslagen                                                   |
| missed backup                   | gemiste back-up                                                |
| warning                         | waarschuwing                                                   |
| restore (noun)                  | herstel                                                        |
| restore (verb)                  | herstellen                                                     |
| restore target                  | hersteldoel                                                    |
| test restore                    | testherstel                                                    |
| overwrite                       | overschrijven                                                  |
| scratch database                | tijdelijke database                                            |
| connection                      | verbinding                                                     |
| destination                     | bestemming                                                     |
| storage                         | opslag                                                         |
| free space                      | vrije ruimte                                                   |
| retention                       | bewaarbeleid                                                   |
| keep last N                     | laatste N bewaren                                              |
| retention hold                  | bewaarblokkade                                                 |
| artifact                        | back-upbestand (technical context: artefact)                   |
| manifest                        | manifest                                                       |
| dump                            | dump                                                           |
| database / table / schema       | database / tabel / schema                                      |
| globals                         | globale objecten                                               |
| completeness evidence           | volledigheidsbewijs                                            |
| schema version                  | schemaversie                                                   |
| encryption / encrypted          | versleuteling / versleuteld                                    |
| passphrase                      | wachtwoordzin                                                  |
| recipient (age)                 | ontvanger                                                      |
| key / private key / public key  | sleutel / privésleutel / publieke sleutel                      |
| key version                     | sleutelversie                                                  |
| certificate                     | certificaat                                                    |
| pin (noun) / pinned             | pin / vastgepind                                               |
| host key                        | hostsleutel                                                    |
| fingerprint                     | vingerafdruk                                                   |
| SSH tunnel / bastion            | SSH-tunnel / bastionhost                                       |
| allowlist                       | toegestane lijst                                               |
| download                        | downloaden / download                                          |
| owner (of a job)                | eigenaar                                                       |
| role: Admin / Maintainer / User | Beheerder / Onderhouder / Gebruiker                            |
| user, account                   | gebruiker, account                                             |
| sign in / sign out              | inloggen / uitloggen                                           |
| session                         | sessie                                                         |
| password / temporary password   | wachtwoord / tijdelijk wachtwoord                              |
| two-factor authentication       | tweestapsverificatie                                           |
| second factor                   | tweede factor                                                  |
| passkey                         | passkey                                                        |
| authenticator app               | authenticator-app                                              |
| recovery code                   | herstelcode                                                    |
| confirm your identity (step-up) | je identiteit bevestigen                                       |
| revoke                          | intrekken                                                      |
| enable / disable                | inschakelen / uitschakelen                                     |
| enabled / disabled              | ingeschakeld / uitgeschakeld                                   |
| settings                        | instellingen                                                   |
| audit log                       | auditlog                                                       |
| security alert                  | beveiligingsmelding                                            |
| accepted risk                   | geaccepteerd risico                                            |
| notification                    | melding                                                        |
| inbox                           | inbox                                                          |
| channel                         | kanaal                                                         |
| webhook / signing key           | webhook / ondertekeningssleutel                                |
| (webhook) delivery              | bezorging                                                      |
| rotate (a key)                  | vernieuwen                                                     |
| summary (e-mail)                | samenvatting (never "overzicht": that is the Overview section) |
| dashboard                       | dashboard                                                      |
| Overview (nav section)          | Overzicht                                                      |
| calendar                        | kalender                                                       |
| API token                       | API-token                                                      |
| address allowlist (API)         | toegestane adressen                                            |
| runner                          | runner                                                         |
| reverse proxy                   | reverse proxy                                                  |
| instance                        | instantie                                                      |
| step / wizard                   | stap / wizard                                                  |

## Terms added during the 8.1 translation

| English                                                                                | Nederlands                                                                 |
| -------------------------------------------------------------------------------------- | -------------------------------------------------------------------------- |
| privileged (role)                                                                      | bevoorrecht (never "geprivilegieerd" or "verhoogde rechten")               |
| deep / standard (verification level)                                                   | grondig / standaard                                                        |
| exact / estimate / approximate (row counts)                                            | exact / schatting / benaderd                                               |
| notices (run, informational)                                                           | opmerkingen (never "meldingen")                                            |
| event                                                                                  | gebeurtenis                                                                |
| finding                                                                                | bevinding                                                                  |
| verdict (test restore, role)                                                           | oordeel                                                                    |
| trigger (of a run)                                                                     | aanleiding                                                                 |
| catch-up run                                                                           | inhaalrun                                                                  |
| attempt                                                                                | poging                                                                     |
| overdue                                                                                | te laat                                                                    |
| paused                                                                                 | gepauzeerd                                                                 |
| projected                                                                              | verwacht                                                                   |
| healthy / never run                                                                    | gezond / nooit uitgevoerd                                                  |
| staging / verifying (restore)                                                          | klaarzetten / controleren                                                  |
| restores (plural noun, page)                                                           | herstelacties; nav item "Herstel"                                          |
| restore history / log                                                                  | herstelgeschiedenis / herstellog                                           |
| target connection                                                                      | doelverbinding                                                             |
| temporary restore connection                                                           | tijdelijke herstelverbinding                                               |
| scratch target                                                                         | tijdelijk hersteldoel                                                      |
| backup owner                                                                           | back-upeigenaar                                                            |
| ownership (owner grant)                                                                | eigenaarschap                                                              |
| shared channel                                                                         | gedeeld kanaal                                                             |
| attach / detach                                                                        | koppelen / ontkoppelen                                                     |
| per-job override / inherited                                                           | uitzondering (per taak) / overgenomen                                      |
| notification settings                                                                  | meldingsinstellingen                                                       |
| notification center                                                                    | meldingencentrum                                                           |
| grace period                                                                           | overgangsperiode                                                           |
| lockout / locked                                                                       | blokkering / geblokkeerd                                                   |
| enrolled (2FA)                                                                         | ingesteld                                                                  |
| credentials                                                                            | inloggegevens                                                              |
| write-only / read-only                                                                 | alleen-schrijven / alleen-lezen                                            |
| endpoint                                                                               | eindpunt                                                                   |
| ready / not ready                                                                      | gereed / niet gereed                                                       |
| keypair / key type                                                                     | sleutelpaar / sleuteltype                                                  |
| out of band                                                                            | via een ander kanaal                                                       |
| self-signed                                                                            | zelfondertekend                                                            |
| certificate pin                                                                        | certificaatpin                                                             |
| leaf / anchor / intermediate CA                                                        | leaf-certificaat / ankercertificaat / tussenliggende CA                    |
| CA bundle / private CA / system roots                                                  | CA-bundel / privé-CA / systeemroots                                        |
| insecure transport                                                                     | onveilig transport                                                         |
| opt-out (of a secure default)                                                          | afwijking van de veilige standaard                                         |
| widened                                                                                | verruimd                                                                   |
| any address                                                                            | elk adres                                                                  |
| never expires                                                                          | verloopt nooit                                                             |
| expiry                                                                                 | vervaldatum / verloopt                                                     |
| disk (destination type)                                                                | schijf                                                                     |
| no-delete mode                                                                         | niet-verwijderen-modus                                                     |
| unrecorded files                                                                       | niet-geregistreerde bestanden                                              |
| storage class / base path / subfolder                                                  | opslagklasse / basispad / submap                                           |
| Object Lock retention days                                                             | bewaartermijn                                                              |
| soft delete                                                                            | zachte verwijdering                                                        |
| preset / custom query / version query                                                  | voorinstelling / eigen query / versiequery                                 |
| capture (schema version)                                                               | vastleggen                                                                 |
| run history                                                                            | rungeschiedenis                                                            |
| run timeout                                                                            | maximale duur van een run                                                  |
| severity: critical / error / warning / info                                            | ernst: kritiek / fout / waarschuwing / info                                |
| condition (of an accepted risk)                                                        | situatie                                                                   |
| ledger                                                                                 | register                                                                   |
| actor / target (audit)                                                                 | uitvoerder / doel                                                          |
| chain hash / signed head / redacted                                                    | ketenhash / ondertekende kop / geredigeerd                                 |
| SSRF guard / allowlist entry                                                           | SSRF-beveiliging / vermelding                                              |
| recovery (disaster, admin page)                                                        | noodherstel (never "herstel": that is restore)                             |
| key escrow / keyring                                                                   | sleutelbewaring / sleutelring                                              |
| live settings / restart required                                                       | live-instellingen / herstart nodig                                         |
| image (container) / pinned by digest                                                   | image / vastgepind op digest                                               |
| scheduler / work queues                                                                | planner / werkwachtrijen                                                   |
| weakened settings                                                                      | verzwakte instellingen                                                     |
| getting started                                                                        | aan de slag                                                                |
| setup (browser setup) / access code / report                                           | installatie / toegangscode / verslag                                       |
| colour theme: light / dark / system                                                    | kleurthema: licht / donker / systeem                                       |
| AWS field names (Access key ID, Secret access key, Path style, Governance, Compliance) | untranslated                                                               |
| warnings (summary status badge)                                                        | let op                                                                     |
| retention hold (summary status badge)                                                  | blokkade                                                                   |
| free space unknown (summary)                                                           | onbekend                                                                   |
| summary frequency: daily / every other day / weekly                                    | dagelijkse samenvatting / samenvatting om de dag / wekelijkse samenvatting |
| partial / disabled (summary status badge)                                              | deels / uit (short words for the badge column)                             |

Server texts (`texts.go`) keep Go's `%q` quoting around names in both
languages (it escapes control characters), so names appear in straight
quotes there, not „…”.
