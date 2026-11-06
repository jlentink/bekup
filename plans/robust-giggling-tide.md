# Plan: a clearer summary e-mail (full period, standout failures, per-job detail)

## Context

The periodic summary (D-226, `internal/notify/summary`) has three problems:

1. **Wrong window.** A daily summary showed 08:00–12:00. `decide` (`schedule.go:204`) starts the period at the previous summary's end, with no minimum length. So after a settings change (for example weekly at 08:00 → daily at 12:00) the first summary covers only a few hours. The preview (`api.go:263` `previewStart`) covers "the current period so far", which is also 08:00 → now. A daily summary must cover the last 24 hours.
2. **Failures look normal.** `email.Notice` has no severity. A failed job is an ordinary `Label: Value` row, styled like the storage rows, and the subject and heading look the same whether everything is fine or not.
3. **Too little detail.** Only jobs that need attention are listed. Jobs that are fine never appear, and there are no sizes, run counts or last-run times. An admin cannot see at a glance that everything is all right.

Intended outcome:
- The summary covers at least one full period: daily 24 h, every other day 48 h, weekly 7 days, monthly one month.
- A problem is visible in the subject, in a coloured banner (with text, not only colour) and in highlighted table rows.
- One row per job shows its status, runs, size and last run, so "all fine" is visible at a glance.

## Design

### 1. The period: at least one full period
- **New helper** `periodBefore(end time.Time, f Frequency, zone *time.Location)` (`schedule.go`). It returns one calendar period before `end` in the user's zone: `AddDate(0,0,-1)` daily, `-2` every other day, `-7` weekly, `(0,-1,0)` monthly. This is correct across DST changes, because a calendar day can be 23 or 25 h.
- **`decide`:** `start = min(lastPeriodEnd, periodBefore(end))`.
  - The period is never shorter than one full period. After an outage it can still be longer, as today with catch-up, and the cap stays at one year.
  - After a settings change the first summary may overlap the previous one. Showing an hour twice is better than missing hours.
  - `previousOccurrence` is no longer needed for the start.
- **`previewStart`:** returns `periodBefore(now)`, so the preview shows "the last 24 h / 7 days up to now", not "since 08:00". The preview still never touches `last_period_end`.
- **Docs and decision:** `docs/guides/notifications.md` ("A summary covers the last full period, more after an outage; it may overlap the previous one after a settings change") and a D-226 amendment.

### 2. HTML e-mail design (all bekup e-mails share the layout)

**Today:** the template is a `div` with `max-width:560px`, inline styles, a grey label/value table and a blue button. It looks plain, ignores dark mode beyond the meta tag, and Outlook desktop (Word engine) ignores `max-width` on a `div`. It is the same for good and bad news.

**Direction: a calm operations report that gets loud only when something is wrong.**
- **Brand:** bekup's own design system (D-021), not a generic template.
  - **Colours:** the web theme's status tokens (`web/src/styles/theme.css`: `--status-success/-partial/-failed/-missed/-neutral` with `-fg` and `-soft`, light and dark) converted to hex, because e-mail clients do not support `oklch`. A comment names the token next to each hex value.
  - **Fonts:** no web fonts load (no remote resources), so we use font stacks.
    - Headings use the display stack (`"Instrument Serif", Georgia, serif`); Georgia is a good fallback.
    - Body text uses the system sans stack.
    - Sizes and counts use the mono stack (`ui-monospace, "SF Mono", Menlo, Consolas, monospace`) with tabular figures, right-aligned, so columns of numbers line up like a ledger.
- **Top status strip:** a 6 px full-width bar in the overall status colour (green / amber / red), then the banner block. The banner has the soft tint background, a 4 px left rule in the status colour, and a large uppercase word ("ALL OK", "2 FAILED", "ATTENTION") followed by one sentence. When everything is fine it stays quiet and small; on failure it is the first thing seen.
- **Key figures:** a row of 3–4 figure tiles: jobs OK `12/14`, failed `2`, data written this period, restores tested. Big mono number, small caps label underneath. On phones they become 2×2.
- **Jobs table:** a real data table with `<th scope="col">`.
  - Each row has a status pill: a text word on a soft tint, with `bgcolor` so Outlook shows it.
  - Problem rows get the soft tint across the row plus a 4 px left border in the status colour. OK rows stay plain, with hairline separators.
  - On narrow screens the "Stored total" and "Runs" columns hide (`.hide-sm` media query) so the status, job and size stay readable.
- **Button:** a bulletproof button (a table cell with `bgcolor`, padding on the link). It works in Outlook without VML and is at least 44 px high.
- **Inbox preview line:** a hidden preheader, for example "2 failed · 12 OK · last 24 h", so the mail list shows the state before the mail is opened.

**Technical rules for the template**, based on how clients render:
- **Layout:**
  - Layout is built with tables (`role="presentation"`): an outer 100% table, an inner 600 px centred table, and inline styles on every element as the baseline.
  - A small `<style>` block in the head carries only progressive enhancements: mobile stacking, `.hide-sm`, and dark mode. Gmail and Apple Mail support it, and Outlook ignores it safely.
- **Outlook desktop:** a fixed-width `<!--[if mso]>` wrapper table. html/template drops HTML comments, so these come from fixed `template.HTML` constants in the package, never from data. A test asserts the constants contain no template input.
- **Dark mode:**
  - `color-scheme` and `supported-color-schemes` meta tags.
  - `@media (prefers-color-scheme: dark)` overrides with the theme's dark tokens, for Apple Mail, iOS and Outlook for Mac.
  - `[data-ogsc]` selectors for Outlook.com.
  - Colours are chosen to survive Gmail's forced inversion: soft tints rather than pure white or black, and text colours with AA contrast both ways.
- **Accessibility:**
  - `lang="en"`;
  - layout tables marked presentation and data tables marked up properly;
  - body text at least 14 px;
  - every status is a word, never only a colour;
  - a test computes WCAG AA contrast for each text/background hex pair, light and dark.
- **Size:** Gmail clips messages above 102 KB. The budget is on the HTML part as sent (quoted-printable, `email.EncodedLen`): at most 90 000 bytes. Rows are capped at 100, and when escaped or multi-byte names would still exceed the budget the summary lists fewer jobs (OK rows first, then excess problem rows) and counts the rest in the "+N more" line (review finding, D-226 amendment 1). Tests cover the 100-row summary and hostile names (`&`, `"`, `<`, emoji).
- **Plain text** stays first-class. Some admins read only plain text, and it is the fallback for every client:
  - the banner line;
  - the key figures;
  - an aligned table with `>>` markers on problem rows.

**Scope:** every bekup e-mail uses the new shared layout: verification, security alerts, problem notifications and the summary. Their content is unchanged, and the old notices' string tests keep passing. The banner, figures and tables are optional parts that only the summary (and later others) fill.

### 2b. E-mail building blocks (`internal/notify/email`)
The changes to `Notice` are additive: the new fields are optional.
- **`Banner *Banner{Level, Word, Text}`**, with `Level` `LevelOK | LevelWarning | LevelFailure`. It renders as the status strip and banner block of section 2. `Word` is the big status word ("ALL OK", "2 FAILED"); the text always carries it, so the meaning never depends on colour.
- **`Figures []Figure{Label, Value, Level}`** (at most 4): the key-figure tiles.
- **`Preheader string`**: the hidden inbox preview line.
- **`Tables []Table{Title string, Columns []string, Rows []Row{Cells []string, Level}}`**.
  - A row with `LevelFailure` gets the failed tint, a 4 px left border and a bold first cell; `LevelWarning` the partial tint. `Table.NumericColumns` marks right-aligned mono columns, and `Table.NarrowHidden` marks the columns that hide on phones.
  - The status cell always holds a word ("Failed", "Partial", "Missed", "OK").
  - Limits are added to `validate`: at most 3 tables, 8 columns, 120 rows, and `maxNoticeField` per cell. Cells may contain no control characters (the existing rule).
- **Plain-text part (`notice.txt.tmpl`):**
  - The banner becomes a first line, for example `!! FAILED: 2 of 14 jobs failed !!`.
  - Tables become aligned columns, with a `>>` marker in front of failure and warning rows.
- Still html/template only: no images, no scripts and no remote CSS or fonts, as the `email.Render` rules in AGENTS.md require. The only raw HTML is the fixed Outlook constants. Tests cover escaping in every new field.

### 3. Summary content (`internal/notify/summary/content.go`, `summary.sql`)
**New per-job query `SummaryJobActivity`** (`internal/db/queries/summary.sql`, sqlc). It uses the same scope rule as the other summary queries (`job_owners` EXISTS for role user). Per job, for runs finished in `(start, end]`:
- counts per status (success, partial, failed, canceled);
- the sum of `bytes_stored`;
- the newest run's `finished_at`, status and `error_code`;
- the longest duration.

It is joined with `JobStorageTotals` (`queries/job_storage.sql:10`, already scoped) for the stored total per job. The number of rows is bounded by the existing job limit (`CountSummaryJobs` / `ListSummaryJobs`).

**Job status for the period**, with one clear rule per job, worst first. `failed` and `partial` reuse the existing `fill` attention rules (`content.go:293`):
- **Failed:** a failed run in the period, or a failed test restore.
- **Missed:** `job_missed_state`, or the existing "scheduled but no success" rule.
- **Partial:** a partial run, or warnings.
- **Held:** retention holds.
- **OK:** at least one success and nothing above.
- **No run:** not scheduled in the period and no run.
- **Disabled.**

**The e-mail, top to bottom:**
1. **Subject.** `bekup daily summary: all 14 jobs OK`, `bekup daily summary: 2 FAILED, 1 missed`, or `… needs attention`. The worst state comes first, so problems are visible in the inbox list.
2. **Banner** with the overall level and one sentence, for example "2 of 14 jobs failed in the last 24 hours".
3. **Period line**, in the user's zone using `summaryTime`: "Sat 7 Nov 2026, 08:00 – Sun 8 Nov 2026, 08:00 (Europe/Amsterdam)".
4. **Table "Jobs"** with the columns Status | Job | Runs (ok/failed) | Last run | Size (this period) | Stored total.
   - Rows are sorted worst first, then by name.
   - Failure rows show the fixed error text of `error_code` (the `jobs.Code*` texts, never server output) in the status cell, for example "Failed: target unreachable".
   - Job names go through `notify.DisplayName`. Sizes use `storage.FormatSize`, replacing the local `formatBytes` copy.
   - Above 100 jobs, every problem row is still listed and the rest becomes one line "+N more jobs, all OK".
5. **Table "Restores"** (only when there were any): restores and test restores in the period, counted by result, with failed test restores as failure rows. The query is an extension of `SummaryTestRestoreCounts` to all restores of jobs in scope.
6. **Table "Storage"** (staff only, `OpStorageUsageView`, as today): destination | stored | free (cached) | backups. A destination with less than 10% free space gets a warning row. That threshold is a constant documented in D-226; if a configurable one exists already (`monitor`), reuse it.
7. **Security alerts** (admins, as today), as a warning row when there were any.
8. **Action button** "Open dashboard" (base_url), as today.

**Inbox row:** still counts only, no names (AGENTS rule), and no staff parts (no security-alert count: the stored row outlives a role change). The title gains the same worst-first wording: "Daily summary: 2 failed, 12 OK".

**Event `Detail`:** unchanged (bounded scalars), plus `catch_up` (`decide`'s `CatchUp`), which alone drives the "previous one could not be sent on time" note. (A `level` field was dropped in review: nothing read it.)

### 4. Decisions and docs
- **D-226 amendment 1 (proposed):**
  - the full-period rule (minimum one period, overlap allowed);
  - the preview window;
  - the per-job table for every job in scope;
  - the status rule and worst-first subject;
  - banner and table rows with text labels plus colour;
  - the 100-row cap with problems always listed;
  - the free-space warning threshold.
- **AGENTS.md:** in the notify/email conventions:
  - `Notice.Banner` / `Figures` / `Tables` / `Preheader`, with levels always paired with words, and the limits;
  - the e-mail layout rules: tables plus inline styles, `<style>` only for enhancements, Outlook wrappers only as fixed constants, colours only from the hex copies of the theme's status tokens (keep them in sync with `theme.css`), and the size budget under Gmail's 102 KB clip. Update the D-226 line in the Notifications section if it names the content.
- **`docs/guides/notifications.md` "Summary e-mail":** the window, what each section shows, and an example (plain text).

## Files
- `internal/notify/summary/schedule.go` (`periodBefore`, `decide`), `api.go` (`previewStart`), `content.go` (data, status rule, build), `content_test.go`, `schedule_test.go`, `summary_integration_test.go`
- `internal/db/queries/summary.sql` (+ `just gen` for sqlc)
- `internal/notify/email/template.go`, `templates/notice.html.tmpl`, `templates/notice.txt.tmpl`, `template_test.go`
- `docs/DECISIONS.md`, `docs/guides/notifications.md`, `AGENTS.md`

## Tests
- **`schedule_test.go` (`TestDecide`):**
  - a settings change from weekly 08:00 to daily 12:00 gives 24 h, not 4 h;
  - an hour change gives 24 h;
  - catch-up after 3 days down gives 72 h;
  - DST days give 23 h / 25 h, one calendar day;
  - monthly gives one calendar month.

  Preview: `previewStart` = `now − one period`.
- **`template_test.go`:**
  - the banner and table render in HTML and text;
  - failure rows carry the marker and the word;
  - escaping in cells;
  - the limits refuse oversize tables;
  - every existing notice (verification, security alert, problem notification) still renders its content, and the existing string tests pass;
  - golden HTML and text files in `internal/notify/email/testdata/` (regenerated with `-update`, reviewed by eye): summary all-OK, summary with failures, the 100-row maximum, a security alert and a verification mail;
  - the 100-row summary stays under 90 KB of HTML as sent (quoted-printable);
  - WCAG AA contrast for every text/background hex pair, light and dark;
  - the Outlook `template.HTML` constants are fixed strings.
- **`content_test.go`:**
  - the all-OK subject, banner and every job listed;
  - mixed failed/missed/partial/OK sorted worst first, with error-code texts;
  - the 100-row cap keeps every problem row;
  - scoping: a user sees only owned jobs, and staff-only sections are hidden for role user;
  - no names in the inbox body.
- **`summary_integration_test.go`:** a seeded day with successes, a failure and a missed job. The rendered e-mail has the right rows and sizes, and `SummaryJobActivity` respects the owner scope.

## Verification
- `just gen`, `just lint` (golangci-lint 2.14.0), `just sec`, `just test`, and `env DOCKER_API_VERSION=1.47 go test -race -tags integration ./internal/notify/...`.
- **By hand:**
  - run `just dev` with mailpit and `just run`;
  - set a daily summary at the current hour plus a preview;
  - with jobs that succeed and one that fails (an unreachable target), look at the e-mail in mailpit in HTML (light and dark) and plain text;
  - use mailpit's "HTML Check" tab (client support scores per feature, based on caniemail) and fix anything flagged as unsupported in major clients;
  - send the preview to real inboxes and check them: Gmail web and the Android/iOS app (forced dark mode), Apple Mail macOS/iOS light and dark, Outlook new/web and, if available, Outlook desktop for Windows;
  - open the golden HTML files in a browser at 600 px and 375 px width;
  - check the window (24 h), the red banner and failure row, the subject, and the per-job sizes;
  - then fix the job and check the next summary shows green "all OK".
- End with a security review agent and a code review agent (milestone rule); the user commits.
