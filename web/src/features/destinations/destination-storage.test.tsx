import { screen, waitFor, within } from "@testing-library/react";
import { beforeAll, describe, expect, it } from "vitest";

import {
  DEST_ID,
  destinationFixture,
  stubResizeObserver,
} from "@/features/connections/test-fixtures";
import { baseHandlers, meFixture, ok, renderApp } from "@/test/mock-api";

beforeAll(stubResizeObserver);

const GiB = 1024 * 1024 * 1024;

function stored(storedBytes: number, backupCount: number, artifactCount = backupCount * 2) {
  return () =>
    ok({
      destinationUsage: [
        {
          __typename: "DestinationUsage",
          destinationId: DEST_ID,
          storedBytes,
          artifactCount,
          backupCount,
        },
      ],
    });
}

function space(overrides: Record<string, unknown> = {}) {
  return () =>
    ok({
      destinationSpace: {
        __typename: "DestinationSpace",
        destinationId: DEST_ID,
        status: "AVAILABLE",
        reason: null,
        totalBytes: 100 * GiB,
        freeBytes: 60 * GiB,
        usedBytes: 40 * GiB,
        newestSetBytes: 2 * GiB,
        low: false,
        checkedAt: "2026-11-05T08:00:00Z",
        ...overrides,
      },
    });
}

const detail = (handlers: Record<string, unknown>) => ({
  ...baseHandlers(meFixture({}, "MAINTAINER")),
  DestinationDetail: () => ok({ destination: destinationFixture("DISK") }),
  ...handlers,
});

describe("destination storage (D-225)", () => {
  it("lists the stored size and backups per destination", async () => {
    renderApp("/destinations", {
      ...baseHandlers(),
      DestinationsList: () => ok({ destinations: [destinationFixture("DISK")] }),
      DestinationStored: stored(5 * GiB, 3),
    });
    const table = await screen.findByRole("table");
    expect(within(table).getByRole("columnheader", { name: "Stored" })).toBeInTheDocument();
    const cell = await within(table).findByTestId("destination-stored");
    expect(cell).toHaveTextContent("5.00 GiB");
    expect(cell).toHaveTextContent("3 backups");
  });

  it("shows bekup's share, the free space and a bar on the detail page", async () => {
    const { callsOf } = renderApp(`/destinations/${DEST_ID}`, {
      ...detail({ DestinationStored: stored(10 * GiB, 4), DestinationSpace: space() }),
    });
    expect(await screen.findByTestId("stored-line")).toHaveTextContent(
      "10.0 GiB used by bekup · 4 backups · 8 files",
    );
    expect(await screen.findByTestId("space-line")).toHaveTextContent(
      "10.0 GiB used by bekup · 60.0 GiB free of 100 GiB",
    );
    expect(
      screen.getByRole("img", { name: /Of 100 GiB: 10.0 GiB bekup's backups/ }),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("space-low")).toBeNull();
    expect(callsOf("DestinationSpace")[0]?.variables).toEqual({ id: DEST_ID });
  });

  it("warns when the free space is low", async () => {
    renderApp(`/destinations/${DEST_ID}`, {
      ...detail({
        DestinationStored: stored(10 * GiB, 4),
        DestinationSpace: space({ freeBytes: 5 * GiB, usedBytes: 95 * GiB, low: true }),
      }),
    });
    const warning = await screen.findByTestId("space-low");
    expect(warning).toHaveTextContent("Free space is low");
    expect(warning).toHaveTextContent("2.00 GiB");
  });

  it("explains a backend that reports no space", async () => {
    renderApp(`/destinations/${DEST_ID}`, {
      ...detail({
        DestinationStored: stored(0, 0),
        DestinationSpace: space({
          status: "UNSUPPORTED",
          reason: "S3 does not report free space: a bucket has no fixed size.",
          totalBytes: null,
          freeBytes: null,
          usedBytes: null,
          checkedAt: null,
        }),
      }),
    });
    const reason = await screen.findByTestId("space-reason");
    expect(reason).toHaveTextContent("Free space is not reported");
    expect(reason).toHaveTextContent("a bucket has no fixed size");
    expect(within(reason).queryByRole("button")).toBeNull();
  });

  it("offers a new check when the space was unavailable", async () => {
    let n = 0;
    const { user } = renderApp(`/destinations/${DEST_ID}`, {
      ...detail({
        DestinationStored: stored(1024, 1),
        DestinationSpace: () => {
          n++;
          return n === 1
            ? space({
                status: "UNAVAILABLE",
                reason: "The destination could not be reached.",
                totalBytes: null,
                freeBytes: null,
                usedBytes: null,
              })()
            : space()();
        },
      }),
    });
    await user.click(await screen.findByRole("button", { name: "Check again" }));
    await waitFor(() => {
      expect(screen.getByTestId("space-line")).toHaveTextContent("60.0 GiB free of 100 GiB");
    });
  });
});
