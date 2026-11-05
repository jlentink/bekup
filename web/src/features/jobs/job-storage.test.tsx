import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { baseHandlers, meFixture, ok, renderApp } from "@/test/mock-api";

import {
  DEST_ID,
  emptyRuns,
  JOB_ID,
  jobDetail,
  jobListNode,
  jobsList,
  noTestRestores,
} from "./test-fixtures";

const MiB = 1024 * 1024;

function storage(overrides: Record<string, unknown> = {}) {
  const run = (i: number, bytes: number, status = "SUCCESS") => ({
    __typename: "JobStorageRun",
    runId: `0b6c3f7e-0000-4000-8000-00000000r${String(i).padStart(3, "0")}`,
    status,
    at: `2026-10-${String(20 + i).padStart(2, "0")}T03:04:00Z`,
    bytes,
  });
  return () =>
    ok({
      job: {
        __typename: "Job",
        id: JOB_ID,
        storage: {
          __typename: "JobStorage",
          totalBytes: 60 * MiB,
          artifactCount: 6,
          backupCount: 3,
          perDestination: [
            {
              __typename: "JobStorageDestination",
              destinationId: DEST_ID,
              name: "vault-s3",
              type: "S3",
              deleted: false,
              storedBytes: 30 * MiB,
              artifactCount: 3,
              backupCount: 3,
            },
            {
              __typename: "JobStorageDestination",
              destinationId: "0b6c3f7e-0000-4000-8000-0000000000d9",
              name: "old-nas",
              type: "SFTP",
              deleted: true,
              storedBytes: 30 * MiB,
              artifactCount: 3,
              backupCount: 3,
            },
          ],
          newestRun: run(3, 12 * MiB),
          trend: [run(1, 8 * MiB), run(2, 4 * MiB, "PARTIAL"), run(3, 12 * MiB)],
          ...overrides,
        },
      },
    });
}

const detailHandlers = {
  JobDetail: () => ok({ job: jobDetail() }),
  RunsList: () => ok(emptyRuns),
  LatestTestRestore: () => ok(noTestRestores),
};

describe("job storage (D-225)", () => {
  it("shows the stored total in the jobs list", async () => {
    renderApp("/jobs", {
      ...baseHandlers(),
      JobFilterConnections: () => ok({ connections: [] }),
      JobsList: () =>
        ok(
          jobsList([
            jobListNode(),
            jobListNode({
              id: "j2",
              slug: "empty",
              name: "Empty",
              storage: { __typename: "JobStorage", totalBytes: 0, backupCount: 0 },
            }),
          ]),
        ),
    });
    expect(await screen.findByRole("columnheader", { name: "Stored" })).toBeInTheDocument();
    const cells = await screen.findAllByTestId("job-stored");
    expect(cells[0]).toHaveTextContent("3.00 MiB");
    expect(cells[1]).toHaveTextContent("None");
  });

  it("shows totals, the newest backup, per-destination rows and the trend (backup owner)", async () => {
    const { callsOf } = renderApp(`/jobs/${JOB_ID}`, {
      ...baseHandlers(meFixture({}, "USER")),
      ...detailHandlers,
      JobStorage: storage(),
    });
    const section = await screen.findByRole("region", { name: "Storage" });
    expect(await within(section).findByTestId("storage-total")).toHaveTextContent("60.0 MiB");
    expect(within(section).getByText(/3 backups · 6 files/)).toBeInTheDocument();
    expect(within(section).getByTestId("storage-newest")).toHaveTextContent("12.0 MiB");
    const rows = within(section).getAllByTestId("storage-destination");
    expect(rows).toHaveLength(2);
    expect(rows[1]).toHaveTextContent(/old-nas/);
    expect(rows[1]).toHaveTextContent(/deleted/);
    const chart = within(section).getByRole("img", {
      name: /last 3 backups, from 4.00 MiB to 12.0 MiB/,
    });
    expect(chart.querySelectorAll("rect")).toHaveLength(3);
    expect(chart.querySelectorAll("rect.fill-status-partial")).toHaveLength(1);
    expect(callsOf("JobStorage")[0]?.variables).toEqual({ id: JOB_ID });
  });

  it("says so when nothing is stored", async () => {
    renderApp(`/jobs/${JOB_ID}`, {
      ...baseHandlers(),
      ...detailHandlers,
      JobStorage: storage({
        totalBytes: 0,
        artifactCount: 0,
        backupCount: 0,
        perDestination: [],
        newestRun: null,
        trend: [],
      }),
    });
    expect(await screen.findByText("No backup files are stored yet.")).toBeInTheDocument();
  });
});
