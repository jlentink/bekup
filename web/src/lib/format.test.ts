import { describe, expect, it } from "vitest";

import { formatBytes, formatClock, formatDuration, formatRelative } from "./format";

describe("formatBytes", () => {
  it.each([
    [0, "0 B"],
    [512, "512 B"],
    [1024, "1.00 KiB"],
    [1536, "1.50 KiB"],
    [3 * 1024 * 1024, "3.00 MiB"],
    [15.5 * 1024 ** 3, "15.5 GiB"],
    [250 * 1024 ** 4, "250 TiB"],
    [2 * 1024 ** 6, "2048 PiB"],
  ])("formats %d bytes in binary units as %s", (bytes, want) => {
    expect(formatBytes(bytes)).toBe(want);
  });

  it.each([null, undefined, -1, Number.NaN, Number.POSITIVE_INFINITY])("is empty for %s", (v) => {
    expect(formatBytes(v)).toBe("");
  });
});

describe("formatDuration", () => {
  it.each([
    [0.4, "0.4s"],
    [42, "42s"],
    [187, "3m 07s"],
    [7500, "2h 05m"],
  ])("formats %d seconds as %s", (s, want) => {
    expect(formatDuration(s)).toBe(want);
  });

  it.each([null, undefined, -3, Number.NaN])("is empty for %s", (v) => {
    expect(formatDuration(v)).toBe("");
  });
});

describe("formatClock / formatRelative", () => {
  it("formats a local wall-clock time", () => {
    const d = new Date(2026, 9, 28, 14, 5, 9);
    expect(formatClock(d.toISOString())).toBe("14:05:09");
    expect(formatClock("nope")).toBe("");
    expect(formatClock(null)).toBe("");
  });

  it("formats relative times", () => {
    const now = Date.parse("2026-10-28T12:00:00Z");
    expect(formatRelative("2026-10-28T09:00:00Z", now)).toMatch(/3 hours ago/);
    expect(formatRelative(undefined, now)).toBe("");
  });
});
