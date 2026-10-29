import { describe, expect, it } from "vitest";

import {
  addMonths,
  bucketByDay,
  civilOf,
  dayKey,
  eventHref,
  jobIdsFor,
  MAX_RANGE_MS,
  moveFocus,
  parseDayKey,
  variantOf,
  viewDays,
  viewRange,
  zonedToUtc,
  type CalEvent,
} from "./model";

function ev(over: Partial<CalEvent> = {}): CalEvent {
  return {
    jobId: "j1",
    jobName: "Shop",
    kind: "PLANNED",
    start: "2026-10-28T20:00:00Z",
    end: null,
    status: null,
    runId: null,
    destinationId: null,
    projected: false,
    paused: false,
    startedEarly: false,
    overdue: false,
    detail: null,
    ...over,
  };
}

describe("time zones", () => {
  it("converts wall-clock midnight to UTC, across DST", () => {
    expect(zonedToUtc({ y: 2026, m: 10, d: 29 }, "Asia/Tokyo").toISOString()).toBe(
      "2026-10-28T15:00:00.000Z",
    );
    // New York: EDT until 1 Nov 2026, EST after.
    expect(zonedToUtc({ y: 2026, m: 10, d: 31 }, "America/New_York").toISOString()).toBe(
      "2026-10-31T04:00:00.000Z",
    );
    expect(zonedToUtc({ y: 2026, m: 11, d: 2 }, "America/New_York").toISOString()).toBe(
      "2026-11-02T05:00:00.000Z",
    );
  });

  it("buckets events by their day in the chosen zone", () => {
    const e = ev();
    expect(civilOf(new Date(e.start), "UTC")).toEqual({ y: 2026, m: 10, d: 28 });
    expect([...bucketByDay([e], "Asia/Tokyo").keys()]).toEqual(["2026-10-29"]);
    expect([...bucketByDay([e], "UTC").keys()]).toEqual(["2026-10-28"]);
  });
});

describe("views", () => {
  it("shows whole Monday-first weeks for a month", () => {
    const days = viewDays("month", { y: 2026, m: 10, d: 14 });
    expect(dayKey(days[0]!)).toBe("2026-09-28");
    expect(dayKey(days[days.length - 1]!)).toBe("2026-11-01");
    expect(days).toHaveLength(35);
    expect(viewDays("week", { y: 2026, m: 10, d: 28 }).map(dayKey)[0]).toBe("2026-10-26");
  });

  it("never asks for more than the API's 93 days", () => {
    for (const m of [1, 2, 3, 6, 8, 12]) {
      const { from, to } = viewRange("month", { y: 2026, m, d: 1 }, "Pacific/Kiritimati");
      expect(to.getTime() - from.getTime()).toBeLessThanOrEqual(MAX_RANGE_MS);
      expect(to.getTime()).toBeGreaterThan(from.getTime());
    }
    const day = viewRange("day", { y: 2026, m: 10, d: 28 }, "UTC");
    expect(day.to.getTime() - day.from.getTime()).toBe(86_400_000);
  });

  it("moves the focus with the keyboard", () => {
    const c = { y: 2026, m: 10, d: 31 };
    expect(moveFocus("month", c, "ArrowRight")).toEqual({ y: 2026, m: 11, d: 1 });
    expect(moveFocus("month", c, "ArrowUp")).toEqual({ y: 2026, m: 10, d: 24 });
    expect(moveFocus("month", c, "Home")).toEqual({ y: 2026, m: 10, d: 26 });
    expect(moveFocus("month", c, "PageDown")).toEqual({ y: 2026, m: 11, d: 30 });
    expect(moveFocus("day", c, "ArrowDown")).toBeUndefined();
    expect(moveFocus("week", c, "x")).toBeUndefined();
    expect(addMonths({ y: 2026, m: 1, d: 31 }, 1)).toEqual({ y: 2026, m: 2, d: 28 });
  });

  it("parses only real dates", () => {
    expect(parseDayKey("2026-02-30")).toBeUndefined();
    expect(parseDayKey("2026-02-28")).toEqual({ y: 2026, m: 2, d: 28 });
    expect(parseDayKey("junk")).toBeUndefined();
  });
});

describe("filters and presentation", () => {
  const jobs = [
    { id: "j1", connection: { id: "c1" }, destinations: [{ id: "d1" }] },
    { id: "j2", connection: { id: "c2" }, destinations: [{ id: "d1" }, { id: "d2" }] },
  ];
  it("maps connection and destination filters to job ids", () => {
    expect(jobIdsFor(jobs, {})).toBeNull();
    expect(jobIdsFor(jobs, { connectionId: "c2" })).toEqual(["j2"]);
    expect(jobIdsFor(jobs, { destinationId: "d1" })).toEqual(["j1", "j2"]);
    expect(jobIdsFor(jobs, { jobId: "j1", destinationId: "d2" })).toEqual([]);
  });

  it("styles and links events by kind", () => {
    expect(variantOf(ev({ paused: true }))).toBe("paused");
    expect(variantOf(ev({ overdue: true }))).toBe("overdue");
    expect(variantOf(ev({ startedEarly: true, runId: "r1" }))).toBe("early");
    expect(variantOf(ev({ kind: "RUN", status: "FAILED" }))).toBe("run-failed");
    expect(variantOf(ev({ kind: "EXPIRY", projected: true }))).toBe("expiry-projected");
    expect(eventHref(ev({ kind: "RUN", runId: "r1" }))).toBe("/runs/r1");
    expect(eventHref(ev())).toBe("/jobs/j1");
  });
});
