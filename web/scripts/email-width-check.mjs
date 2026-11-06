#!/usr/bin/env node
// E-mail width check (D-227 amendment 1): every golden e-mail of the Go
// tests (internal/notify/summary/testdata/*.html and
// internal/notify/email/testdata/*.html) must fit a 375 px phone screen
// in light and dark mode: the document may not scroll sideways
// (document.documentElement.scrollWidth <= 375).
//
// A plain `--window-size=375` headless screenshot is clipped (headless
// Chrome keeps a minimum window of about 500 px), so the page gets a real
// 375 px viewport through playwright.
//
// Usage: pnpm csp-check            (runs this after the CSP check)
//        node scripts/email-width-check.mjs [--shots <dir> [--suffix <s>]]
//        CHROME_PATH=/path/to/chrome node scripts/email-width-check.mjs
//
// --shots writes full-page screenshots of every golden at 375 px (light
// and dark) and 600 px (light) into <dir> for review.
import { readdirSync } from "node:fs";
import { basename, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

import { chromium } from "playwright-core";

const PHONE_WIDTH = 375;

const repo = resolve(fileURLToPath(new URL("../..", import.meta.url)));
const goldenDirs = [
  join(repo, "internal/notify/summary/testdata"),
  join(repo, "internal/notify/email/testdata"),
];

function goldens() {
  const files = [];
  for (const dir of goldenDirs) {
    for (const name of readdirSync(dir).sort()) {
      if (name.endsWith(".html")) files.push(join(dir, name));
    }
  }
  return files;
}

function args() {
  const out = { shots: "", suffix: "" };
  const argv = process.argv.slice(2);
  for (let i = 0; i < argv.length; i++) {
    if (argv[i] === "--shots") out.shots = argv[++i] ?? "";
    else if (argv[i] === "--suffix") out.suffix = argv[++i] ?? "";
    else throw new Error(`unknown argument ${argv[i]}`);
  }
  return out;
}

async function launch() {
  const executablePath = process.env.CHROME_PATH;
  return chromium.launch(executablePath ? { executablePath } : { channel: "chrome" });
}

async function measure(browser, file, width, colorScheme, shot) {
  const page = await browser.newPage({ viewport: { width, height: 800 }, colorScheme });
  try {
    // Golden e-mails are self-contained (no remote content); block any
    // request but the file itself.
    await page.route("**/*", (route) =>
      route.request().url() === pathToFileURL(file).href ? route.continue() : route.abort(),
    );
    await page.goto(pathToFileURL(file).href, { waitUntil: "load" });
    const scrollWidth = await page.evaluate(() => document.documentElement.scrollWidth);
    if (shot) await page.screenshot({ path: shot, fullPage: true });
    return scrollWidth;
  } finally {
    await page.close();
  }
}

async function main() {
  const { shots, suffix } = args();
  const files = goldens();
  if (files.length === 0) throw new Error("no golden e-mails found");
  const browser = await launch();
  const problems = [];
  try {
    for (const file of files) {
      const name = basename(file, ".html");
      const widths = [];
      for (const scheme of ["light", "dark"]) {
        const shot = shots ? join(shots, `${name}-375-${scheme}${suffix}.png`) : "";
        const w = await measure(browser, file, PHONE_WIDTH, scheme, shot);
        widths.push(`${scheme} ${w}`);
        if (w > PHONE_WIDTH)
          problems.push(`${name} (${scheme}): ${w} px wide at a ${PHONE_WIDTH} px viewport`);
      }
      if (shots) await measure(browser, file, 600, "light", join(shots, `${name}-600-light${suffix}.png`));
      console.log(`  ${name}: ${widths.join(", ")} (viewport ${PHONE_WIDTH})`);
    }
  } finally {
    await browser.close();
  }
  if (problems.length) {
    for (const p of problems) console.error(`  ✗ ${p}`);
    console.error(`\nemail-width-check FAILED (${problems.length} problem(s)).`);
    process.exit(1);
  }
  console.log(`email-width-check passed: ${files.length} golden e-mails fit ${PHONE_WIDTH} px (light, dark).`);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
