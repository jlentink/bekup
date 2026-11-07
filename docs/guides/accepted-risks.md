# Accepted security risks

bekup marks every weakened setting and reminds you of it (D-001). When you
cannot fix one, because the environment is not yours to change, you can
**accept the risk** for one month, one year or without an end date
(D-228). An accepted risk is no longer an open issue: it leaves the open
findings of the dashboard's Security tile, the daily reminder e-mails stop
and the startup alert is not sent. It stays **marked**: the badge turns
into a muted "Accepted until …" (or "Accepted, no end date") and the
plain-HTTP banner stays for everyone.

## What can be accepted

| Risk | Where it shows | Who may accept |
|---|---|---|
| bekup served over plain HTTP (`server.base_url` is http) | banner, configuration | admins |
| App database TLS mode `require` or `disable` | configuration page | admins |
| Single-account database mode | configuration page | admins |
| The bekup image follows a tag instead of a digest | configuration page | admins |
| SMTP mode `auto` or `none` (e-mail may be sent unencrypted) | SMTP settings | admins |
| A connection's TLS mode UNVERIFIED or DISABLED | connections, dashboard | admins |
| A connection's role is privileged, or its privileges are unknown | the connection's last test, dashboard | admins |
| A job's backups are not encrypted (opted out) | the job, dashboard | admins and the job's backup owners |

Maintainers see the connection and job risks and their acceptances but
never accept or revoke; the instance and SMTP risks are for admins only.
Backup owners see and decide only about the jobs they own.

## Accepting a risk

Choose **Accept risk** next to the badge, on the dashboard's Security tile,
or on the **Accepted risks** page (Overview → Accepted risks). The dialog
shows the exact condition, asks for the duration and a reason (1–500
characters, visible to everyone who sees the risk) and confirms your
identity (step-up). The decision is audited and every admin is alerted
(`risk_accepted`; without an end date at error level,
`risk_accepted_permanently`).

An acceptance covers **exactly the condition you saw**. If anything that
makes it a different risk changes, the risk is flagged again:

- a connection's TLS mode, host, port or route (SSH tunnel on or off, or
  another bastion host, port or user), and for a privileged role the
  privilege verdict, its reasons or the role name;
- a job that opts out of encryption again later;
- the SMTP mode, host, port or username;
- the app database's TLS mode, CA choice or host, the base URL.

A failed connection test stores no privilege verdict; that does not end
an acceptance of a privileged role.

If the condition changed while the dialog was open, bekup refuses the
acceptance and asks you to review the risk again.

Accepting a privileged role does **not** remove the acknowledgement that
restores and test restores into that role need.

## How acceptances end

- **Revoke** on the Accepted risks page ends it at once (step-up, audited).
- One month and one year acceptances **expire**. Seven days before, the
  admins and the person who accepted it get `risk_acceptance_expiring`;
  when it ends, `risk_acceptance_ended`.
- An acceptance also ends when the **condition changes**, when the
  connection or job is deleted or the risk is fixed, and when the person
  who accepted it may no longer decide (disabled, deleted, demoted, or no
  longer an owner of the job).

Accepting again replaces the current acceptance. The page keeps every
ended acceptance with its reason; nothing is deleted.

## Summary e-mail

Admins' periodic summary lists, in the Security table, the number of
accepted risks, those without an end date and the external API tokens
without expiry (D-229). These are never counted as problems.

## Several processes

`serve` and `bekup worker` read their own `bekup.toml`. An instance
acceptance covers a process only when that process has exactly the
accepted condition; a worker with a different setting keeps reminding.
