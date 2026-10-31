import { describe, expect, it } from "vitest";

import { estimateRows, holdReason, holdSentence, retentionInput } from "./retention-model";

describe("retentionInput", () => {
  it.each([
    ["7", "", { keepLast: 7 }],
    ["", "30", { keepDays: 30 }],
    [" 7 ", "30", { keepLast: 7, keepDays: 30 }],
    ["", "", null],
    ["0", "", null],
    ["7", "x", null],
    ["1.5", "", null],
    ["-1", "", null],
  ] as const)("keepLast %j, keepDays %j", (last, days, want) => {
    expect(retentionInput(last, days)).toEqual(want);
  });
});

describe("estimateRows", () => {
  it("shows each rule and the union with bars relative to the largest", () => {
    const rows = estimateRows(
      { kept: 30, byKeepLast: 7, byKeepDays: 30, atLeast: false },
      { keepLast: 7, keepDays: 30 },
    );
    expect(rows.map((r) => [r.key, r.count, r.off, r.share])).toEqual([
      ["last", 7, false, 7 / 30],
      ["days", 30, false, 1],
      ["total", 30, false, 1],
    ]);
  });

  it("marks unset rules off and unknown counts null", () => {
    const rows = estimateRows(
      { kept: null, byKeepLast: 0, byKeepDays: null, atLeast: false },
      { keepDays: 14 },
    );
    expect(rows.map((r) => [r.key, r.count, r.off, r.share])).toEqual([
      ["last", 0, true, 0],
      ["days", null, false, 0],
      ["total", null, false, 0],
    ]);
  });
});

describe("hold sentences", () => {
  const base = {
    destinationName: "vault-s3",
    destinationDeleted: false,
    reason: "NO_COMPLETE_RUN" as const,
    heldRuns: 3,
    since: "2026-10-20T03:00:00Z",
    newestCompleteAt: null,
  };

  it("explains a destination without a complete backup", () => {
    expect(holdReason(base)).toBe("no complete backup there");
    expect(holdSentence(base)).toMatch(
      /^Retention is holding 3 incomplete backups on vault-s3 since .+: no complete backup there\.$/,
    );
  });

  it("names the newest complete backup and deleted destinations", () => {
    const h = {
      ...base,
      reason: "NEWER_THAN_NEWEST_COMPLETE" as const,
      heldRuns: 1,
      destinationDeleted: true,
      newestCompleteAt: "2026-10-18T03:00:00Z",
    };
    expect(holdReason(h)).toMatch(/^no complete backup there since .+/);
    expect(holdSentence(h)).toMatch(/1 incomplete backup on vault-s3 \(deleted\) since/);
  });
});
