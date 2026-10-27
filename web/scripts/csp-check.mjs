#!/usr/bin/env node
// CSP regression check (task 0.8, see docs/DECISIONS.md "CSP for the web UI").
//
// 1. Builds the UI with the dev-only /design style guide enabled into
//    .csp-check-dist/ (never shipped).
// 2. Serves it with the production Content-Security-Policy.
// 3. Drives headless Chrome through every page and overlay component and
//    fails if the browser reports any CSP violation.
// 4. Opens a same-origin WebSocket to /graphql (the subscription transport,
//    task 1.10): `connect-src 'self'` must allow it (CSP3 matches ws:/wss:
//    of the page's host), while a cross-origin WebSocket must be blocked.
//
// Usage: pnpm csp-check            (uses the installed Google Chrome)
//        CHROME_PATH=/path/to/chrome pnpm csp-check
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, readFileSync, statSync } from "node:fs";
import { createServer } from "node:http";
import { extname, join, normalize, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { chromium } from "playwright-core";

const STEP_TIMEOUT_MS = 10_000;

/** Keep in sync with docs/DECISIONS.md and internal/httpserver (task 1.1). */
export const CSP = [
  "default-src 'self'",
  "script-src 'self'",
  "style-src 'self'",
  "img-src 'self'",
  "font-src 'self'",
  "connect-src 'self'",
  "object-src 'none'",
  "base-uri 'none'",
  "form-action 'self'",
  "frame-ancestors 'none'",
  "worker-src 'none'",
  "manifest-src 'self'",
  "require-trusted-types-for 'script'",
  "trusted-types 'none'",
].join("; ");

const root = resolve(fileURLToPath(new URL("..", import.meta.url)));
const outDir = join(root, ".csp-check-dist");

function build() {
  const res = spawnSync("pnpm", ["exec", "vite", "build", "--logLevel", "warn"], {
    cwd: root,
    stdio: "inherit",
    env: { ...process.env, VITE_ENABLE_DESIGN: "true", BEKUP_WEB_OUT_DIR: outDir },
  });
  if (res.status !== 0) throw new Error("vite build failed");
}

function assertNoInlineCode() {
  const html = readFileSync(join(outDir, "index.html"), "utf8");
  const problems = [];
  if (/<script(?![^>]*\bsrc=)[^>]*>/i.test(html)) problems.push("inline <script> in index.html");
  if (/<style[\s>]/i.test(html)) problems.push("inline <style> in index.html");
  if (/\sstyle=/i.test(html)) problems.push("style= attribute in index.html");
  if (/\son[a-z]+=/i.test(html)) problems.push("inline event handler in index.html");
  return problems;
}

const TYPES = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".svg": "image/svg+xml",
  ".woff2": "font/woff2",
  ".woff": "font/woff",
};

function serve() {
  const server = createServer((req, res) => {
    const url = new URL(req.url ?? "/", "http://localhost");
    let file = normalize(join(outDir, decodeURIComponent(url.pathname)));
    if (!file.startsWith(outDir)) {
      res.writeHead(400).end();
      return;
    }
    if (!existsSync(file) || statSync(file).isDirectory()) file = join(outDir, "index.html");
    res.writeHead(200, {
      "Content-Type": TYPES[extname(file)] ?? "application/octet-stream",
      "Content-Security-Policy": CSP,
      "X-Content-Type-Options": "nosniff",
      "Referrer-Policy": "no-referrer",
    });
    res.end(readFileSync(file));
  });
  // Minimal WebSocket handshake on /graphql so the browser can open the
  // socket (no frames are exchanged).
  server.on("upgrade", (req, socket) => {
    const key = req.headers["sec-websocket-key"];
    if (
      new URL(req.url ?? "/", "http://localhost").pathname !== "/graphql" ||
      typeof key !== "string"
    ) {
      socket.destroy();
      return;
    }
    const accept = createHash("sha1")
      .update(`${key}258EAFA5-E914-47DA-95CA-C5AB0DC85B11`)
      .digest("base64");
    const offered = String(req.headers["sec-websocket-protocol"] ?? "")
      .split(",")
      .map((p) => p.trim());
    const lines = [
      "HTTP/1.1 101 Switching Protocols",
      "Upgrade: websocket",
      "Connection: Upgrade",
      `Sec-WebSocket-Accept: ${accept}`,
    ];
    if (offered.includes("graphql-transport-ws"))
      lines.push("Sec-WebSocket-Protocol: graphql-transport-ws");
    socket.write(`${lines.join("\r\n")}\r\n\r\n`);
    setTimeout(() => socket.destroy(), 2000);
  });
  return new Promise((ok) => {
    server.listen(0, "127.0.0.1", () => {
      ok(server);
    });
  });
}

async function launch() {
  const executablePath = process.env.CHROME_PATH;
  return chromium.launch(executablePath ? { executablePath } : { channel: "chrome" });
}

async function exercise(page, base, colorScheme) {
  await page.emulateMedia({ colorScheme });

  await page.goto(`${base}/login`);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.getByText("Enter your e-mail address.").waitFor();
  await page.getByRole("button", { name: /passkey/ }).click();
  await page.locator("[data-sonner-toast]").first().waitFor();

  await page.goto(`${base}/`);
  await page.getByRole("button", { name: "Account menu" }).click();
  await page.getByRole("menu").waitFor();
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "Notifications" }).hover();
  await page.getByRole("tooltip").waitFor();
  await page.goto(`${base}/jobs`);
  await page.goto(`${base}/does-not-exist`);

  await page.goto(`${base}/design`);
  await page.getByRole("radio", { name: "Dark" }).first().check({ force: true });
  await page.getByTestId("ds-dialog").click();
  await page.getByRole("dialog").waitFor();
  // The static replacement for react-remove-scroll's injected styles works.
  const locked = await page.evaluate(
    () =>
      document.body.hasAttribute("data-scroll-locked") &&
      getComputedStyle(document.body).overflow === "hidden",
  );
  if (!locked) throw new Error("body scroll lock not applied while a dialog is open");
  await page.keyboard.press("Escape");
  await page.getByTestId("ds-menu").click();
  await page.getByRole("menu").waitFor();
  await page.keyboard.press("Escape");
  await page.getByTestId("ds-select").click();
  await page.getByRole("listbox").waitFor();
  await page.keyboard.press("Escape");
  await page.getByTestId("ds-tooltip").hover();
  await page.getByRole("tooltip").waitFor();
  await page.getByTestId("ds-toast").click();
  await page.locator("[data-sonner-toast]").first().waitFor();
  await page.getByRole("tab", { name: "Cards" }).click();
  await page.getByRole("radio", { name: "Light" }).first().check({ force: true });
  await page.waitForTimeout(300);
}

/** Opens a WebSocket from the page; reports whether it opened and whether a connect-src violation fired. */
function openWebSocket(page, url) {
  return page.evaluate(
    (target) =>
      new Promise((ok) => {
        let violated = false;
        document.addEventListener("securitypolicyviolation", (e) => {
          if (e.effectiveDirective === "connect-src") violated = true;
        });
        const done = (open) => setTimeout(() => ok({ open, violated }), 200);
        try {
          const ws = new WebSocket(target, "graphql-transport-ws");
          ws.onopen = () => {
            ws.close();
            done(true);
          };
          ws.onerror = () => done(false);
        } catch {
          done(false);
        }
        setTimeout(() => ok({ open: false, violated }), 3000);
      }),
    url,
  );
}

async function main() {
  build();
  const problems = assertNoInlineCode();
  const server = await serve();
  const { port } = server.address();
  const base = `http://127.0.0.1:${port}`;
  const browser = await launch();
  const violations = [];
  try {
    for (const scheme of ["light", "dark"]) {
      const page = await browser.newPage();
      page.setDefaultTimeout(STEP_TIMEOUT_MS);
      await page.addInitScript(() => {
        window.__csp = [];
        document.addEventListener("securitypolicyviolation", (e) => {
          window.__csp.push(
            `${e.effectiveDirective} blocked ${e.blockedURI || "inline"} at ${e.sourceFile}:${e.lineNumber} ${e.sample}`,
          );
        });
      });
      let recording = true;
      page.on("console", (msg) => {
        if (recording && /Content.Security.Policy|Trusted.Type/i.test(msg.text())) {
          violations.push(`[${scheme}] console: ${msg.text()}`);
        }
      });
      page.on("pageerror", (err) => violations.push(`[${scheme}] page error: ${err.message}`));

      try {
        await exercise(page, base, scheme);
      } catch (err) {
        problems.push(
          `[${scheme}] UI walk-through failed at ${page.url()}: ${err.message.split("\n")[0]}`,
        );
      }
      // Same-origin WebSocket (subscriptions): must open without a violation.
      const sameOrigin = await openWebSocket(page, `ws://127.0.0.1:${port}/graphql`);
      if (!sameOrigin.open || sameOrigin.violated)
        problems.push(
          `[${scheme}] same-origin WebSocket: open=${sameOrigin.open} violation=${sameOrigin.violated}`,
        );
      const seen = await page.evaluate(() => window.__csp);
      violations.push(...seen.map((v) => `[${scheme}] ${v}`));

      // Positive control: the harness must notice a violation when one happens.
      recording = false;
      const control = await page.evaluate(
        () =>
          new Promise((ok) => {
            document.addEventListener("securitypolicyviolation", () => ok(true), { once: true });
            const s = document.createElement("style");
            s.append("body{}");
            document.head.append(s);
            setTimeout(() => ok(false), 2000);
          }),
      );
      if (!control)
        problems.push(`[${scheme}] positive control: injected <style> was NOT reported`);
      // Positive control: a cross-origin WebSocket (other host name) is blocked.
      const crossOrigin = await openWebSocket(page, `ws://localhost:${port}/graphql`);
      if (crossOrigin.open || !crossOrigin.violated)
        problems.push(
          `[${scheme}] cross-origin WebSocket was NOT blocked by connect-src (open=${crossOrigin.open})`,
        );
      await page.close();
    }
  } finally {
    await browser.close();
    server.close();
  }

  const unique = [...new Set(violations)];
  console.log(`\nCSP: ${CSP}\n`);
  if (problems.length || unique.length) {
    for (const p of [...problems, ...unique]) console.error(`  ✗ ${p}`);
    console.error(`\ncsp-check FAILED (${problems.length + unique.length} problem(s)).`);
    process.exit(1);
  }
  console.log(
    "csp-check passed: no CSP or Trusted Types violations in light and dark mode; same-origin WebSocket allowed, cross-origin blocked.",
  );
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
