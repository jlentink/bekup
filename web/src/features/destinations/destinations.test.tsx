import { screen, waitFor, within } from "@testing-library/react";
import { beforeAll, describe, expect, it } from "vitest";

import {
  DEST_ID,
  destinationFixture,
  stepUpHandlers,
  stubResizeObserver,
} from "@/features/connections/test-fixtures";
import { baseHandlers, gqlError, meFixture, ok, renderApp, type Vars } from "@/test/mock-api";

import { sftpNeedsPassword, toUpdateInput, valuesFrom, type Original } from "./form-model";

beforeAll(stubResizeObserver);

const maintainer = meFixture({}, "MAINTAINER");

const FILE_A = "0b6c3f7e-0000-4000-8000-0000000000f1";
const FILE_B = "0b6c3f7e-0000-4000-8000-0000000000f2";
const FILE_C = "0b6c3f7e-0000-4000-8000-0000000000f3";

function unrecordedFile(id: string, key: string, size = 1048576) {
  return {
    __typename: "UnrecordedFile",
    id,
    key,
    size,
    modifiedAt: "2026-10-01T03:10:00Z",
    firstSeenAt: "2026-10-03T02:00:00Z",
    lastSeenAt: "2026-10-28T02:00:00Z",
  };
}

/** A DestinationUnrecordedFiles page (D-209). */
function unrecorded(
  files: ReturnType<typeof unrecordedFile>[],
  overrides: Record<string, unknown> = {},
) {
  return ok({
    destinationUnrecordedFiles: {
      __typename: "UnrecordedFiles",
      destinationId: DEST_ID,
      scannedAt: "2026-10-28T02:00:00Z",
      totalCount: files.length,
      totalBytes: files.reduce((n, f) => n + f.size, 0),
      truncated: false,
      prunedDirectories: 0,
      files,
      nextCursor: null,
      ...overrides,
    },
  });
}

const noUnrecorded = () => unrecorded([]);

describe("destinations list", () => {
  it("shows type and protection modes; maintainers get no create action", async () => {
    renderApp("/destinations", {
      ...baseHandlers(maintainer),
      DestinationsList: () =>
        ok({
          destinations: [
            destinationFixture("S3", {}, false),
            destinationFixture(
              "SFTP",
              { id: "6f1c3e0a-0000-4000-8000-0000000000d2", noDelete: true },
              false,
            ),
          ],
        }),
    });
    const table = await screen.findByRole("table");
    expect(within(table).getByText(/Object Lock · Compliance 30d/)).toBeInTheDocument();
    expect(within(table).getByText("No-delete")).toBeInTheDocument();
    expect(within(table).getAllByText("Not tested")).toHaveLength(2);
    expect(screen.queryByRole("link", { name: "New destination" })).toBeNull();
  });

  it("shows the stored last test with its code and stale flag (D-187)", async () => {
    renderApp("/destinations", {
      ...baseHandlers(),
      DestinationsList: () =>
        ok({
          destinations: [
            destinationFixture("S3", {
              lastTest: {
                __typename: "DestinationLastTest",
                at: "2026-10-27T09:00:00Z",
                ok: false,
                code: "permission_denied",
                stale: true,
              },
            }),
          ],
        }),
    });
    const cell = await screen.findByTestId("last-test");
    expect(within(cell).getByText("Failed")).toBeInTheDocument();
    expect(within(cell).getByText("Stale")).toBeInTheDocument();
    expect(within(cell).getByText(/permission_denied/)).toBeInTheDocument();
  });
});

describe("destination detail", () => {
  it("tests a destination and shows the normalized steps (maintainer, read only)", async () => {
    const { user } = renderApp(`/destinations/${DEST_ID}`, {
      ...baseHandlers(maintainer),
      DestinationDetail: () => ok({ destination: destinationFixture("S3", {}, false) }),
      TestDestination: () =>
        ok({
          testDestination: {
            ok: false,
            probeKey: "bekup/.bekup-probe-1",
            probeRemains: true,
            note: null,
            steps: [
              { name: "connect", ok: true, code: "ok", message: "" },
              { name: "write", ok: false, code: "permission_denied", message: "access denied" },
            ],
          },
        }),
    });
    expect(await screen.findByRole("heading", { name: "offsite-s3" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Edit" })).toBeNull();
    expect(screen.queryByRole("button", { name: /Delete destination/ })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Test destination" }));
    const steps = await screen.findByRole("list", { name: "Test steps" });
    expect(within(steps).getByText("access denied")).toBeInTheDocument();
    expect(within(steps).getByText("permission_denied")).toBeInTheDocument();
    expect(screen.getByText(/was left in place/)).toBeInTheDocument();
  });

  it("fetches and pins an SFTP host key", async () => {
    const state = { stepped: false };
    const { user, callsOf } = renderApp(`/destinations/${DEST_ID}`, {
      ...baseHandlers(),
      ...stepUpHandlers(state),
      DestinationDetail: () => ok({ destination: destinationFixture("SFTP") }),
      DestinationUnrecordedFiles: noUnrecorded,
      FetchDestinationHostKey: () =>
        ok({
          fetchDestinationHostKey: {
            __typename: "SshHostKey",
            type: "ssh-ed25519",
            fingerprintSha256: "SHA256:nasfingerprint",
            authorizedKey: "ssh-ed25519 AAAAnas",
            confirmedAt: null,
            host: "files.example",
            port: 22,
          },
        }),
      ConfirmDestinationHostKey: () =>
        state.stepped
          ? ok({
              confirmDestinationHostKey: { __typename: "Destination", id: DEST_ID, ready: true },
            })
          : gqlError("STEP_UP_REQUIRED", "please confirm your identity again"),
    });
    await user.click(await screen.findByRole("button", { name: "Fetch and confirm host key" }));
    const dialog = await screen.findByRole("dialog", { name: "Confirm the host key" });
    await user.click(within(dialog).getByRole("button", { name: "Fetch host key" }));
    await within(dialog).findByText("SHA256:nasfingerprint");
    await user.click(within(dialog).getByLabelText(/I compared this fingerprint/));
    await user.click(within(dialog).getByRole("button", { name: "Pin this host key" }));
    const stepUp = await screen.findByRole("dialog", { name: "Confirm your identity" });
    await user.type(within(stepUp).getByLabelText("6-digit code"), "654321");
    await user.click(within(stepUp).getByRole("button", { name: "Confirm" }));
    await waitFor(() => {
      expect(callsOf("ConfirmDestinationHostKey")).toHaveLength(2);
    });
    expect(callsOf("ConfirmDestinationHostKey")[1]?.variables).toMatchObject({
      expectedHost: "files.example",
      expectedPort: 22,
    });
  });

  it("lists the jobs that block a delete", async () => {
    const { user } = renderApp(`/destinations/${DEST_ID}`, {
      ...baseHandlers(),
      DestinationDetail: () => ok({ destination: destinationFixture("DISK") }),
      DestinationUnrecordedFiles: noUnrecorded,
      DeleteDestination: () =>
        gqlError("CONFLICT", "the destination is still used by these jobs: nightly"),
    });
    await user.click(await screen.findByRole("button", { name: "Delete destination" }));
    const dialog = await screen.findByRole("dialog", { name: /Delete the destination/ });
    await user.click(within(dialog).getByRole("button", { name: "Delete destination" }));
    const jobs = await within(dialog).findByRole("list", { name: "Jobs using it" });
    expect(within(jobs).getByText("nightly")).toBeInTheDocument();
  });
});

describe("unrecorded files (D-209)", () => {
  it("lists the files with totals, scan facts and load more; the banner warns", async () => {
    const { user, callsOf } = renderApp(`/destinations/${DEST_ID}`, {
      ...baseHandlers(),
      DestinationDetail: () => ok({ destination: destinationFixture("DISK") }),
      DestinationUnrecordedFiles: (v: Vars) =>
        v["after"] === "next-1"
          ? unrecorded([unrecordedFile(FILE_C, "crm/20261002T030000Z/crm.dump.zst.age")], {
              totalCount: 3,
              totalBytes: 3 * 1048576,
            })
          : unrecorded(
              [
                unrecordedFile(FILE_A, "billing-db/20261001T030000Z/billing.dump.zst.age"),
                unrecordedFile(FILE_B, "billing-db/20261001T030000Z/manifest.json"),
              ],
              {
                totalCount: 3,
                totalBytes: 3 * 1048576,
                truncated: true,
                prunedDirectories: 4,
                nextCursor: "next-1",
              },
            ),
    });
    const banner = await screen.findByTestId("unrecorded-banner");
    expect(banner).toHaveTextContent("3 unrecorded files (3.00 MiB) on this destination");
    expect(within(banner).getByRole("link", { name: "Review the files" })).toHaveAttribute(
      "href",
      "#unrecorded-files",
    );
    const section = screen.getByRole("region", { name: "Unrecorded files" });
    expect(within(section).getByText(/bekup never deletes them on its own/)).toBeInTheDocument();
    expect(within(section).getByTestId("unrecorded-totals")).toHaveTextContent(
      "3 unrecorded files · 3.00 MiB",
    );
    expect(within(section).getByText(/this list is incomplete/)).toBeInTheDocument();
    expect(within(section).getByText("4")).toBeInTheDocument();
    const table = within(section).getByRole("table", { name: "Unrecorded files" });
    expect(within(table).getAllByTestId("unrecorded-file")).toHaveLength(2);
    expect(within(table).getAllByText("1.00 MiB")).toHaveLength(2);
    expect(callsOf("DestinationUnrecordedFiles")[0]?.variables).toMatchObject({
      destinationId: DEST_ID,
      first: 50,
    });

    await user.click(within(section).getByRole("button", { name: "Load more" }));
    expect(
      await within(table).findByText("crm/20261002T030000Z/crm.dump.zst.age"),
    ).toBeInTheDocument();
    expect(within(table).getAllByTestId("unrecorded-file")).toHaveLength(3);
    expect(within(section).queryByRole("button", { name: "Load more" })).toBeNull();
    expect(within(section).getByText("3 of 3 shown")).toBeInTheDocument();
  });

  it("shows every file once when the refetched first page overlaps loaded pages", async () => {
    const state = { stepped: true };
    let deleted = false;
    const a = unrecordedFile(FILE_A, "billing-db/20261001T030000Z/billing.dump.zst.age");
    const b = unrecordedFile(FILE_B, "billing-db/20261001T030000Z/manifest.json");
    const c = unrecordedFile(FILE_C, "crm/20261002T030000Z/crm.dump.zst.age");
    const { user } = renderApp(`/destinations/${DEST_ID}`, {
      ...baseHandlers(),
      ...stepUpHandlers(state),
      DestinationDetail: () => ok({ destination: destinationFixture("DISK") }),
      // Pages of two: [A, B] then [C]; after A is deleted the first page
      // is [B, C], overlapping the loaded second page.
      DestinationUnrecordedFiles: (v: Vars) =>
        v["after"] === "next-1"
          ? unrecorded([c], { totalCount: 3 })
          : deleted
            ? unrecorded([b, c], { totalCount: 2 })
            : unrecorded([a, b], { totalCount: 3, nextCursor: "next-1" }),
      DeleteUnrecordedFile: () => {
        deleted = true;
        return ok({
          deleteUnrecordedFile: {
            __typename: "UnrecordedFileDeleteResult",
            ok: true,
            code: "ok",
            message: "",
          },
        });
      },
    });
    const section = await screen.findByRole("region", { name: "Unrecorded files" });
    await user.click(await within(section).findByRole("button", { name: "Load more" }));
    const table = within(section).getByRole("table", { name: "Unrecorded files" });
    expect(await within(table).findByText(c.key)).toBeInTheDocument();
    await user.click(within(section).getByRole("button", { name: `Delete ${a.key}` }));
    const dialog = await screen.findByRole("dialog", { name: "Delete this unrecorded file?" });
    await user.click(within(dialog).getByRole("button", { name: "Delete file" }));
    expect(await within(section).findByText(`Deleted ${a.key}.`)).toBeInTheDocument();
    await waitFor(() => {
      expect(within(table).getAllByTestId("unrecorded-file")).toHaveLength(2);
    });
    expect(within(table).getAllByText(c.key)).toHaveLength(1);
    expect(within(table).queryByText(a.key)).toBeNull();
  });

  it("deletes a file behind a confirmation and a step-up, then reloads the list", async () => {
    const state = { stepped: false };
    let deleted = false;
    const key = "billing-db/20261001T030000Z/billing.dump.zst.age";
    const { user, callsOf } = renderApp(`/destinations/${DEST_ID}`, {
      ...baseHandlers(),
      ...stepUpHandlers(state),
      DestinationDetail: () => ok({ destination: destinationFixture("DISK") }),
      DestinationUnrecordedFiles: () => unrecorded(deleted ? [] : [unrecordedFile(FILE_A, key)]),
      DeleteUnrecordedFile: () => {
        if (!state.stepped)
          return gqlError("STEP_UP_REQUIRED", "please confirm your identity again");
        deleted = true;
        return ok({
          deleteUnrecordedFile: {
            __typename: "UnrecordedFileDeleteResult",
            ok: true,
            code: "ok",
            message: "",
          },
        });
      },
    });
    await user.click(await screen.findByRole("button", { name: `Delete ${key}` }));
    const dialog = await screen.findByRole("dialog", { name: "Delete this unrecorded file?" });
    expect(within(dialog).getByText(key)).toBeInTheDocument();
    expect(callsOf("DeleteUnrecordedFile")).toHaveLength(0);
    await user.click(within(dialog).getByRole("button", { name: "Delete file" }));
    const stepUp = await screen.findByRole("dialog", { name: "Confirm your identity" });
    await user.type(within(stepUp).getByLabelText("6-digit code"), "654321");
    await user.click(within(stepUp).getByRole("button", { name: "Confirm" }));
    await waitFor(() => {
      expect(callsOf("DeleteUnrecordedFile")).toHaveLength(2);
    });
    expect(callsOf("DeleteUnrecordedFile")[1]?.variables).toEqual({
      destinationId: DEST_ID,
      id: FILE_A,
    });
    const section = screen.getByRole("region", { name: "Unrecorded files" });
    expect(await within(section).findByText(/No unrecorded files/)).toBeInTheDocument();
    expect(within(section).getByText(`Deleted ${key}.`)).toBeInTheDocument();
    expect(screen.queryByTestId("unrecorded-banner")).toBeNull();
  });

  it("shows refusals and storage failures; the file stays", async () => {
    let attempt = 0;
    const key = "billing-db/20261001T030000Z/manifest.json";
    const { user } = renderApp(`/destinations/${DEST_ID}`, {
      ...baseHandlers(),
      DestinationDetail: () => ok({ destination: destinationFixture("DISK") }),
      DestinationUnrecordedFiles: () => unrecorded([unrecordedFile(FILE_B, key)]),
      DeleteUnrecordedFile: () => {
        attempt++;
        return attempt === 1
          ? gqlError("CONFLICT", "a backup record references this file now")
          : ok({
              deleteUnrecordedFile: {
                __typename: "UnrecordedFileDeleteResult",
                ok: false,
                code: "permission_denied",
                message: "The destination refused the request.",
              },
            });
      },
    });
    const section = await screen.findByRole("region", { name: "Unrecorded files" });
    const del = await within(section).findByRole("button", { name: `Delete ${key}` });
    await user.click(del);
    let dialog = await screen.findByRole("dialog", { name: "Delete this unrecorded file?" });
    await user.click(within(dialog).getByRole("button", { name: "Delete file" }));
    expect(await within(section).findByRole("alert")).toHaveTextContent(
      `${key} was not deleted: A backup record references this file now.`,
    );

    await user.click(del);
    dialog = await screen.findByRole("dialog", { name: "Delete this unrecorded file?" });
    await user.click(within(dialog).getByRole("button", { name: "Delete file" }));
    await waitFor(() => {
      expect(within(section).getByRole("alert")).toHaveTextContent(
        `${key} was not deleted: The destination refused the request.`,
      );
    });
    expect(within(section).getAllByTestId("unrecorded-file")).toHaveLength(1);
  });

  it("disables deleting in no-delete mode", async () => {
    const key = "billing-db/20261001T030000Z/manifest.json";
    renderApp(`/destinations/${DEST_ID}`, {
      ...baseHandlers(),
      DestinationDetail: () => ok({ destination: destinationFixture("DISK", { noDelete: true }) }),
      DestinationUnrecordedFiles: () => unrecorded([unrecordedFile(FILE_B, key)]),
    });
    const section = await screen.findByRole("region", { name: "Unrecorded files" });
    expect(await within(section).findByRole("button", { name: `Delete ${key}` })).toBeDisabled();
    expect(
      within(section).getByText(/no-delete mode: bekup cannot delete these files/),
    ).toBeInTheDocument();
  });

  it("is never queried or shown for maintainers", async () => {
    const { callsOf } = renderApp(`/destinations/${DEST_ID}`, {
      ...baseHandlers(maintainer),
      DestinationDetail: () => ok({ destination: destinationFixture("DISK", {}, false) }),
      DestinationUnrecordedFiles: () =>
        unrecorded([unrecordedFile(FILE_A, "billing-db/20261001T030000Z/manifest.json")]),
    });
    expect(await screen.findByRole("heading", { name: "local-disk" })).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Unrecorded files" })).toBeNull();
    expect(screen.queryByTestId("unrecorded-banner")).toBeNull();
    expect(callsOf("DestinationUnrecordedFiles")).toHaveLength(0);
  });
});

describe("destination form", () => {
  it("creates an S3 destination with Object Lock; the secret key is write-only", async () => {
    const { user, callsOf } = renderApp("/destinations/new", {
      ...baseHandlers(),
      DestinationsList: () => ok({ destinations: [] }),
      DestinationDetail: () => ok({ destination: destinationFixture("S3") }),
      CreateDestination: () =>
        ok({ createDestination: { __typename: "Destination", id: DEST_ID, name: "offsite-s3" } }),
    });
    await user.type(await screen.findByLabelText("Name"), "offsite-s3");
    await user.type(screen.getByLabelText("Region"), "eu-central-1");
    await user.type(screen.getByLabelText("Bucket"), "acme-backups");
    await user.type(screen.getByLabelText("Access key ID"), "AKIAEXAMPLE");
    await user.click(screen.getByRole("button", { name: "Create destination" }));
    expect(await screen.findByText("Enter the secret access key.")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Secret access key"), "wJalrXUtnFEMI-secret");
    await user.click(screen.getByRole("switch", { name: "Lock every upload" }));
    await user.type(screen.getByLabelText("Retention (days)"), "30");
    await user.click(screen.getByRole("button", { name: "Create destination" }));
    await waitFor(() => {
      expect(callsOf("CreateDestination")).toHaveLength(1);
    });
    expect(callsOf("CreateDestination")[0]?.variables).toMatchObject({
      input: {
        name: "offsite-s3",
        type: "S3",
        noDelete: false,
        s3: {
          region: "eu-central-1",
          bucket: "acme-backups",
          accessKeyId: "AKIAEXAMPLE",
          secretAccessKey: "wJalrXUtnFEMI-secret",
          objectLockMode: "GOVERNANCE",
          objectLockRetentionDays: 30,
        },
      },
    });
    await screen.findByRole("heading", { name: "offsite-s3" });
    expect(document.body.textContent).not.toContain("wJalrXUtnFEMI-secret");
  });

  it("maps server validation errors to fields", async () => {
    const { user } = renderApp(`/destinations/${DEST_ID}/edit`, {
      ...baseHandlers(),
      DestinationDetail: () => ok({ destination: destinationFixture("S3") }),
      UpdateDestination: () => gqlError("VALIDATION", "the bucket does not exist", "s3.bucket"),
    });
    await screen.findByLabelText("Bucket");
    // D-209: the location can only change while no backups are stored there.
    expect(screen.getByTestId("location-note")).toHaveTextContent(
      /location can only change while no backups are stored here/,
    );
    // The stored secret access key is never displayed.
    expect(screen.queryByLabelText("New secret access key")).toBeNull();
    expect(screen.getByText("Stored")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    expect(await screen.findByText("The bucket does not exist.")).toBeInTheDocument();
    expect(screen.getByLabelText("Bucket")).toHaveAttribute("aria-invalid", "true");
  });

  it("shows the refusal of a location change at the path field (D-209)", async () => {
    const { user } = renderApp(`/destinations/${DEST_ID}/edit`, {
      ...baseHandlers(),
      DestinationDetail: () => ok({ destination: destinationFixture("DISK") }),
      UpdateDestination: () =>
        gqlError(
          "VALIDATION",
          "the location cannot change while backup files are stored there",
          "disk.path",
        ),
    });
    const path = await screen.findByLabelText("Directory below the disk root");
    expect(screen.getByTestId("location-note")).toBeInTheDocument();
    await user.clear(path);
    await user.type(path, "weekly");
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    expect(
      await screen.findByText("The location cannot change while backup files are stored there."),
    ).toBeInTheDocument();
    expect(path).toHaveAttribute("aria-invalid", "true");
  });

  it("asks for the SFTP password again when the server changes (D-140)", async () => {
    const { user, callsOf } = renderApp(`/destinations/${DEST_ID}/edit`, {
      ...baseHandlers(),
      DestinationDetail: () => ok({ destination: destinationFixture("SFTP") }),
      DestinationsList: () => ok({ destinations: [] }),
      UpdateDestination: () =>
        ok({ updateDestination: { __typename: "Destination", id: DEST_ID, name: "nas-sftp" } }),
    });
    const host = await screen.findByLabelText("Host");
    await user.clear(host);
    await user.type(host, "files2.example");
    expect(await screen.findByText(/The server, user or base path changed/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    expect(await screen.findByText(/enter the password again, or set a key/)).toBeInTheDocument();
    expect(callsOf("UpdateDestination")).toHaveLength(0);
    await user.type(screen.getByLabelText("Password"), "fresh-pass-1");
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => {
      expect(callsOf("UpdateDestination")).toHaveLength(1);
    });
    expect(callsOf("UpdateDestination")[0]?.variables).toMatchObject({
      input: { sftp: { host: "files2.example", password: "fresh-pass-1" } },
    });
  });
});

describe("destination form model", () => {
  it("keeps credentials out of an unchanged update", () => {
    const orig = destinationFixture("SFTP") as unknown as Original;
    const v = valuesFrom(orig);
    expect(sftpNeedsPassword(v, orig)).toBe(false);
    expect(toUpdateInput(v, orig)).toEqual({
      sftp: { host: "files.example", port: 22, user: "bekup", basePath: "/srv/backups", dir: "" },
    });
    expect(sftpNeedsPassword({ ...v, sftp: { ...v.sftp, basePath: "/other" } }, orig)).toBe(true);
  });
});
