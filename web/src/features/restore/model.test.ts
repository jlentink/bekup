import { describe, expect, it } from "vitest";

import { extractIdentity } from "@/lib/age-identity";

import {
  artifactInput,
  canAdvance,
  confirmationMatches,
  globalsInput,
  initialState,
  neutralizedFunctions,
  newDatabaseProblem,
  previewInput,
  previewPasses,
  privilegeGate,
  sortWarnings,
  stepsFor,
  warningSeverity,
  type WizardState,
} from "./model";

const ART = "a0000000-0000-4000-8000-000000000001";
const TGT = "c0000000-0000-4000-8000-000000000001";
const KEY = "AGE-SECRET-KEY-1QYQSZQGPQYQSZQGPQYQSZQGPQYQSZQGPQYQSZQGPQYQSZQGPQYQSZQGPQQ";

function state(over: Partial<WizardState> = {}): WizardState {
  return {
    ...initialState(ART),
    kind: "DATABASE",
    engine: "POSTGRES",
    targetId: TGT,
    targetReady: true,
    newDatabaseAllowed: true,
    ...over,
  };
}

describe("steps", () => {
  it("skips the options for globals", () => {
    expect(stepsFor("GLOBALS")).not.toContain("options");
    expect(stepsFor("DATABASE")).toEqual([
      "source",
      "target",
      "options",
      "preview",
      "confirm",
      "progress",
    ]);
  });
});

describe("canAdvance", () => {
  it("needs a verified source", () => {
    expect(canAdvance("source", { ...initialState(), kind: undefined }, "")).toBe(false);
    expect(canAdvance("source", state(), "")).toBe(true);
  });

  it("needs a ready target and a database choice", () => {
    expect(canAdvance("target", state({ targetReady: false, newDatabase: "x" }), "")).toBe(false);
    expect(canAdvance("target", state({ newDatabase: "" }), "")).toBe(false);
    expect(canAdvance("target", state({ newDatabase: "shop_copy" }), "")).toBe(true);
    expect(
      canAdvance("target", state({ newDatabase: "shop_copy", newDatabaseAllowed: false }), ""),
    ).toBe(false);
    expect(canAdvance("target", state({ mode: "OVERWRITE" }), "")).toBe(false);
    expect(canAdvance("target", state({ mode: "OVERWRITE", existingDatabase: "shop" }), "")).toBe(
      true,
    );
    expect(canAdvance("target", state({ kind: "GLOBALS" }), "")).toBe(true);
  });

  it("needs a well-formed identity only for recipient-encrypted backups", () => {
    expect(canAdvance("options", state(), "")).toBe(true);
    expect(canAdvance("options", state({ needsIdentity: true }), "")).toBe(false);
    expect(canAdvance("options", state({ needsIdentity: true }), "not a key")).toBe(false);
    expect(canAdvance("options", state({ needsIdentity: true }), `# comment\n${KEY}\n`)).toBe(true);
  });
});

describe("new database names", () => {
  it.each([
    ["", "Enter a name for the new database."],
    ["a".repeat(64), "Use at most 63 bytes for the database name."],
    ["bad\u0001", "The name contains control characters."],
    ["a/b", "Avoid quotes, slashes, backslashes and dots in the name."],
    ["shop__bekup_restore_1", "That name is reserved by bekup."],
    ["shop_restored", undefined],
  ])("%j → %j", (name, problem) => {
    expect(newDatabaseProblem(name)).toBe(problem);
  });
});

describe("identity", () => {
  it("finds the key line of an identity file", () => {
    const file = `# created: 2026-10-28\n# public key: age1qyqszqgpqyqszqgp\n${KEY}\n`;
    expect(extractIdentity(file)).toBe(KEY);
    expect(extractIdentity("age1qyqszqgpqyqszqgp")).toBeNull();
  });

  it("is sent only in the restore request, and only when needed", () => {
    const s = state({ needsIdentity: true, newDatabase: "copy" });
    expect(previewInput(s)).not.toHaveProperty("ageIdentity");
    expect(artifactInput(s, KEY, false)).toMatchObject({ ageIdentity: KEY });
    expect(artifactInput(state({ newDatabase: "copy" }), KEY, false)).not.toHaveProperty(
      "ageIdentity",
    );
  });
});

describe("request inputs", () => {
  it("confirms overwrites with the typed name and passes the acknowledgement", () => {
    const s = state({
      mode: "OVERWRITE",
      existingDatabase: "shop",
      confirmation: "shop",
      acknowledgePrivileged: true,
      keepOwners: true,
    });
    expect(artifactInput(s, "", true)).toEqual({
      artifactId: ART,
      targetConnectionId: TGT,
      mode: "OVERWRITE",
      targetDatabase: "shop",
      confirmTargetName: "shop",
      keepOwners: true,
      acknowledgePrivilegedTarget: true,
    });
  });

  it("keeps keepOwners PostgreSQL-only", () => {
    const s = state({ engine: "MYSQL", newDatabase: "copy", keepOwners: true });
    expect(previewInput(s)).not.toHaveProperty("keepOwners");
  });

  it("builds a globals request with the phrase", () => {
    const s = state({ kind: "GLOBALS", confirmation: "restore globals into pg-main" });
    expect(previewInput(s)).toEqual({ artifactId: ART, targetConnectionId: TGT });
    expect(globalsInput(s, false)).toEqual({
      artifactId: ART,
      targetConnectionId: TGT,
      confirmPhrase: "restore globals into pg-main",
    });
  });

  it("matches the confirmation exactly", () => {
    expect(confirmationMatches("shop", "shop")).toBe(true);
    expect(confirmationMatches("shop", "Shop")).toBe(false);
    expect(confirmationMatches("shop", "shop ")).toBe(false);
    expect(confirmationMatches(null, "")).toBe(true);
  });
});

describe("privileged targets", () => {
  const required = { acknowledgementRequired: true };
  it("lets only admins pass, after the acknowledgement", () => {
    expect(privilegeGate({ acknowledgementRequired: false }, "USER", false)).toBe("clear");
    expect(privilegeGate(required, "MAINTAINER", true)).toBe("blocked");
    expect(privilegeGate(required, "USER", true)).toBe("blocked");
    expect(privilegeGate(required, "ADMIN", false)).toBe("needs-ack");
    expect(privilegeGate(required, "ADMIN", true)).toBe("acknowledged");
    expect(previewPasses("blocked")).toBe(false);
    expect(previewPasses("needs-ack")).toBe(false);
    expect(previewPasses("acknowledged")).toBe(true);
  });
});

describe("warnings", () => {
  it("puts destructive warnings first", () => {
    expect(warningSeverity("overwrite_in_place")).toBe("destructive");
    expect(warningSeverity("restore_hardening")).toBe("info");
    expect(warningSeverity("client_version")).toBe("caution");
    expect(
      sortWarnings([{ code: "restore_hardening" }, { code: "overwrite_swap" }]).map((w) => w.code),
    ).toEqual(["overwrite_swap", "restore_hardening"]);
  });

  it("reads the neutralized routines from the log", () => {
    expect(
      neutralizedFunctions([
        { line: "2 SECURITY DEFINER function(s) or procedure(s) were switched" },
        { line: "switched to SECURITY INVOKER: public.esc()" },
        { line: "switched to SECURITY INVOKER: billing.charge(integer)" },
        { line: "... and 3 more" },
      ]),
    ).toEqual({ functions: ["public.esc()", "billing.charge(integer)"], more: 3 });
  });
});
