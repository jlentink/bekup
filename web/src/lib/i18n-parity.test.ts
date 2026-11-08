import { describe, expect, it } from "vitest";

import { definedTables } from "./i18n";

// Load every module that may define a message table (entry points, tests,
// generated code and the dev-only design page excluded).
import.meta.glob(
  [
    "/src/**/*.{ts,tsx}",
    "!/src/**/*.test.{ts,tsx}",
    "!/src/test/**",
    "!/src/graphql/gen/**",
    "!/src/main.tsx",
    "!/src/setup/main.tsx",
    "!/src/routes/design.tsx",
  ],
  { eager: true },
);

/** Differences between the English and the Dutch shape of one table. */
function differences(en: unknown, nl: unknown, path: string, out: string[]): void {
  if (typeof en !== typeof nl || Array.isArray(en) !== Array.isArray(nl)) {
    out.push(`${path}: ${typeof en} in English, ${typeof nl} in Dutch`);
    return;
  }
  if (typeof en === "function" && typeof nl === "function") {
    if (en.length !== nl.length)
      out.push(
        `${path}: ${String(en.length)} parameter(s) in English, ${String(nl.length)} in Dutch`,
      );
    return;
  }
  if (typeof en === "string" && typeof nl === "string") {
    if (en.trim() !== "" && nl.trim() === "") out.push(`${path}: empty in Dutch`);
    return;
  }
  if (en === null || nl === null || typeof en !== "object" || typeof nl !== "object") return;
  const enKeys = Object.keys(en);
  const nlKeys = Object.keys(nl);
  for (const k of enKeys) if (!nlKeys.includes(k)) out.push(`${path}.${k}: missing in Dutch`);
  for (const k of nlKeys) if (!enKeys.includes(k)) out.push(`${path}.${k}: only in Dutch`);
  for (const k of enKeys) {
    if (nlKeys.includes(k))
      differences(
        (en as Record<string, unknown>)[k],
        (nl as Record<string, unknown>)[k],
        `${path}.${k}`,
        out,
      );
  }
}

describe("message tables (D-235)", () => {
  it("are recorded for every module", () => {
    expect(definedTables.length).toBeGreaterThan(80);
  });

  it("catches what the compiler lets through", () => {
    const out: string[] = [];
    differences(
      { f: (n: number) => String(n), a: "A", list: ["x"] },
      { f: () => "", b: "B", list: ["", ""] },
      "t",
      out,
    );
    expect(out).toEqual([
      "t.a: missing in Dutch",
      "t.b: only in Dutch",
      "t.f: 1 parameter(s) in English, 0 in Dutch",
      "t.list.1: only in Dutch",
      "t.list.0: empty in Dutch",
    ]);
  });

  it("have the same keys, parameters and structure in Dutch", () => {
    const out: string[] = [];
    definedTables.forEach((t, i) => {
      differences(t.en, t.nl, `table[${String(i)}]`, out);
    });
    expect(out).toEqual([]);
  });
});
