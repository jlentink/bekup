# bekup web UI

React + TypeScript single-page app, embedded in the `bekup` binary through
`embed.go`. For setup, scripts, the GraphQL client and CSP rules, see
[docs/development.md](../docs/development.md#web-ui-web).

## Visual direction: "Vault Ledger"

bekup looks like a well-kept ledger in a vault: calm, precise and quietly
authoritative. Light mode is ink on warm vellum, and dark mode (a first-class
theme) is paper-white on vault slate. A single **ledger-ink blue** accent marks
interaction and "running". Every other hue belongs to run status, so status
never competes with chrome.

- **Type:** _Instrument Serif_ for page titles and the wordmark,
  _Instrument Sans_ for the interface, and _JetBrains Mono_ (tabular,
  slashed zero) for anything an operator copies or compares: hashes, sizes,
  timestamps, cron, and the small uppercase "eyebrow" labels. All fonts are
  self-hosted.
- **Status vocabulary:** success, partial, failed, running, missed and queued.
  Each pairs a distinct glyph with a color and a text label (`StatusBadge`,
  `StatusDot`), so color is never the only signal.
- **Security:** a diagonal **hazard hatch** in insecure-orange is reserved for
  weakened settings (`SecurityBadge`: _Unencrypted_, _Insecure transport_).
  These badges are permanent (D-001), and the hatch is never used decoratively.
- **Density:** 14 px UI text, 4 px spacing grid, hairline rules, small radii
  (6 px), and almost no shadows except on overlays.
- **Accessibility:** WCAG 2.2 AA. The contrast of every token pair in both
  themes is enforced by `src/styles/theme.test.ts`. Visible focus rings,
  `prefers-reduced-motion`, a skip link, and keyboard-operable controls.

Run `pnpm dev`, then open `/design` for the living style guide (dev-only).
Tokens are in `src/styles/theme.css`, and the Tailwind mapping is in
`src/styles/index.css`.

## Layout

```
src/app/              providers, the route table, RootLayout (auth events, step-up host)
src/features/<area>/  one folder per area (routes, pages, operations, model, strings, tests):
                      auth, account, dashboard, calendar, jobs, runs, restore, connections,
                      destinations, users, notifications, admin
src/components/ui/    shadcn/ui primitives, restyled to the tokens
src/components/       brand, shell (sidebar, top bar, role nav, banners), status, ssh dialogs,
                      theme toggle, shared dialogs and empty states
src/routes/           pages (login, second factor, profile, 404, /design)
src/lib/              Apollo client, API errors, route access, WebAuthn, QR, return paths,
                      shell strings, formatting, theme, CSP shim
src/styles/           tokens, Tailwind theme, CSP compatibility CSS
src/test/             fake GraphQL server (renderApp), WebAuthn and matchMedia stubs
vite-plugins/         build-time CSP hardening
scripts/              csp-check (walks the real flows against a fake API), codegen post-processing
```

## Sign-in and account pages

The sign-in is a ledger in three steps (password, second factor, signed
in), shown as mono step markers above the form. Every failure reads the
same; lockouts show the server's wait time. Accounts with a temporary
password or an overdue 2FA enrollment see only that flow, on the same
vault layout, with a sign-out button. The profile has four tabs (account
and password, two-factor, sessions, notifications) whose sections are
ledger rows: a heading column with a state pill, and the controls. One-time
secrets (the TOTP key, recovery codes) appear once in a dashed "slip" and
are dropped from memory after confirmation. The step-up dialog is shared
by every page (`useStepUp`); see [docs/development.md](../docs/development.md#web-ui-web).
