/**
 * Design-token accessibility gate (WCAG 2.2 AA) for both themes.
 * Text pairs need 4.5:1; UI components and graphical objects (status marks,
 * input borders, focus ring) need 3:1.
 */
import { describe, expect, it } from "vitest";

import { contrastRatio, mixOklch, parseOklch, type Oklch } from "@/lib/color";

import css from "./theme.css?raw";

function block(selector: string): Record<string, string> {
  const start = css.indexOf(`${selector} {`);
  if (start < 0) throw new Error(`selector ${selector} not found`);
  const body = css.slice(start, css.indexOf("\n}", start));
  const vars: Record<string, string> = {};
  for (const m of body.matchAll(/--([\w-]+):\s*([^;]+);/g)) {
    if (m[1] && m[2]) vars[m[1]] = m[2].trim();
  }
  return vars;
}

const light = block(":root");
const themes = { light, dark: { ...light, ...block(".dark") } } as const;

function resolver(vars: Record<string, string>) {
  return function color(name: string): Oklch {
    let value = vars[name];
    for (let i = 0; value?.startsWith("var(") && i < 5; i++) {
      value = vars[value.slice(6, -1)];
    }
    if (!value) throw new Error(`token --${name} is not defined`);
    return parseOklch(value);
  };
}

const TEXT_PAIRS: readonly [fg: string, bg: string][] = [
  ["foreground", "background"],
  ["foreground", "surface"],
  ["foreground", "surface-sunken"],
  ["muted-foreground", "background"],
  ["muted-foreground", "surface"],
  ["muted-foreground", "muted"],
  ["primary", "background"],
  ["primary", "surface"],
  ["primary-foreground", "primary"],
  ["secondary-foreground", "secondary"],
  ["accent-foreground", "accent"],
  ["destructive", "background"],
  ["destructive", "surface"],
  ["destructive-foreground", "destructive"],
  ["sidebar-foreground", "sidebar"],
  ["sidebar-muted-foreground", "sidebar"],
  ["sidebar-accent-foreground", "sidebar-accent"],
];

const NON_TEXT_PAIRS: readonly [fg: string, bg: string][] = [
  ["input", "background"],
  ["input", "surface"],
  ["ring", "background"],
  ["ring", "surface"],
  ["border-strong", "background"],
];

const STATUSES = ["success", "partial", "failed", "running", "missed", "insecure", "neutral"];

describe.each(Object.entries(themes))("%s theme", (_name, vars) => {
  const color = resolver(vars);

  it.each(TEXT_PAIRS)("text %s on %s ≥ 4.5:1", (fg, bg) => {
    expect(contrastRatio(color(fg), color(bg))).toBeGreaterThanOrEqual(4.5);
  });

  it.each(NON_TEXT_PAIRS)("ui %s on %s ≥ 3:1", (fg, bg) => {
    expect(contrastRatio(color(fg), color(bg))).toBeGreaterThanOrEqual(3);
  });

  it.each(STATUSES)("status %s: label ≥ 4.5:1 on its badge, mark ≥ 3:1 on surfaces", (s) => {
    expect(
      contrastRatio(color(`status-${s}-fg`), color(`status-${s}-soft`)),
    ).toBeGreaterThanOrEqual(4.5);
    expect(contrastRatio(color(`status-${s}`), color("background"))).toBeGreaterThanOrEqual(3);
    expect(contrastRatio(color(`status-${s}`), color("surface"))).toBeGreaterThanOrEqual(3);
  });

  it("security badge label stays ≥ 4.5:1 on the darker hatch stripe", () => {
    // Mirrors .hatch-insecure in index.css: color-mix(in oklch, insecure 14%, insecure-soft).
    const stripe = mixOklch(color("status-insecure"), color("status-insecure-soft"), 0.14);
    expect(contrastRatio(color("status-insecure-fg"), stripe)).toBeGreaterThanOrEqual(4.5);
  });
});
