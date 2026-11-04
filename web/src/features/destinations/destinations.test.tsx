import { screen, waitFor, within } from "@testing-library/react";
import { beforeAll, describe, expect, it } from "vitest";

import {
  DEST_ID,
  destinationFixture,
  stepUpHandlers,
  stubResizeObserver,
} from "@/features/connections/test-fixtures";
import { baseHandlers, gqlError, meFixture, ok, renderApp, type Vars } from "@/test/mock-api";

import {
  sftpNeedsPassword,
  toUpdateInput,
  valuesFrom,
  webdavNeedsPassword,
  type Original,
} from "./form-model";

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

const SPKI = "0123456789abcdef".repeat(4);
const CERT_SHA = "fedcba9876543210".repeat(4);

function webdavCert(overrides: Record<string, unknown> = {}) {
  return {
    __typename: "WebdavCertificate",
    spkiSha256: SPKI,
    spkiSha256Base64: "ASNFZ4mrze8BI0VniavN7wEjRWeJq83vASNFZ4mrze8=",
    fingerprintSha256: CERT_SHA,
    notBefore: "2026-01-01T00:00:00Z",
    notAfter: "2036-01-01T00:00:00Z",
    selfSigned: true,
    ...overrides,
  };
}

/** A WebDAV destination with its settings overridden. */
function webdavDestination(webdav: Record<string, unknown> = {}, admin = true) {
  const d = destinationFixture("WEBDAV", {}, admin);
  if (!d.settings) return d;
  return { ...d, settings: { ...d.settings, webdav: { ...d.settings.webdav, ...webdav } } };
}

/** A failed WebDAV test: self-signed, untrusted, Nextcloud seen, overwrite ignored. */
function webdavTest(webdav: Record<string, unknown> = {}) {
  return ok({
    testDestination: {
      __typename: "DestinationTestResult",
      ok: false,
      probeKey: null,
      probeRemains: false,
      note: null,
      steps: [
        { __typename: "DestinationTestStep", name: "connect", ok: true, code: "ok", message: "" },
        {
          __typename: "DestinationTestStep",
          name: "overwrite",
          ok: false,
          code: "overwrite_ignored",
          message: "The server ignores Overwrite: F.",
        },
      ],
      webdav: {
        __typename: "WebdavTestDetails",
        trust: "SYSTEM",
        trusted: false,
        certificate: webdavCert(),
        pinMatches: false,
        server: "NEXTCLOUD",
        suggestedFlavor: "NEXTCLOUD",
        uploadMode: null,
        overwriteProtection: "IGNORED",
        ...webdav,
      },
    },
  });
}

describe("WebDAV destinations (D-222)", () => {
  it("creates a Nextcloud WebDAV destination through the form", async () => {
    const { user, callsOf } = renderApp("/destinations/new", {
      ...baseHandlers(),
      DestinationsList: () => ok({ destinations: [] }),
      DestinationDetail: () => ok({ destination: webdavDestination() }),
      DestinationUnrecordedFiles: noUnrecorded,
      CreateDestination: () =>
        ok({ createDestination: { __typename: "Destination", id: DEST_ID, name: "cloud-dav" } }),
    });
    await user.click(await screen.findByRole("radio", { name: /^WebDAV/ }));
    await user.type(screen.getByLabelText("Name"), "cloud-dav");
    // The URL help follows the server type.
    expect(screen.getByText(/existing folder that holds the backups/)).toBeInTheDocument();
    await user.click(screen.getByRole("radio", { name: /^Nextcloud \/ ownCloud/ }));
    expect(screen.getByText(/https:\/\/name\.stackstorage\.com/)).toBeInTheDocument();
    expect(screen.getByText(/Basic auth, over HTTPS only/)).toBeInTheDocument();
    expect(screen.getByText(/use an app password/)).toBeInTheDocument();

    await user.type(screen.getByLabelText("URL"), "http//cloud.example.com");
    await user.type(screen.getByLabelText("Request timeout (seconds, optional)"), "4");
    await user.click(screen.getByRole("button", { name: "Create destination" }));
    expect(
      await screen.findByText("Enter an https:// URL (http only in dev mode)."),
    ).toBeInTheDocument();
    expect(screen.getByText("Enter the username.")).toBeInTheDocument();
    expect(
      screen.getByText("Enter a whole number of seconds between 5 and 600."),
    ).toBeInTheDocument();
    expect(callsOf("CreateDestination")).toHaveLength(0);

    const url = screen.getByLabelText("URL");
    await user.clear(url);
    await user.type(url, "https://cloud.example.com");
    await user.type(screen.getByLabelText("Username"), "bekup");
    await user.type(screen.getByLabelText("Password"), "app-pass-123");
    await user.type(screen.getByLabelText("Folder"), "Backups/bekup");
    const timeout = screen.getByLabelText("Request timeout (seconds, optional)");
    await user.clear(timeout);
    await user.type(timeout, "120");
    await user.click(screen.getByRole("button", { name: "Create destination" }));
    await waitFor(() => {
      expect(callsOf("CreateDestination")).toHaveLength(1);
    });
    expect(callsOf("CreateDestination")[0]?.variables).toEqual({
      input: {
        name: "cloud-dav",
        type: "WEBDAV",
        noDelete: false,
        webdav: {
          url: "https://cloud.example.com",
          flavor: "NEXTCLOUD",
          username: "bekup",
          password: "app-pass-123",
          path: "Backups/bekup",
          timeoutSeconds: 120,
        },
      },
    });
    await screen.findByRole("heading", { name: "cloud-dav" });
    expect(document.body.textContent).not.toContain("app-pass-123");
  });

  it("asks for the password again when the username changes; maps server field errors", async () => {
    let attempt = 0;
    const { user, callsOf } = renderApp(`/destinations/${DEST_ID}/edit`, {
      ...baseHandlers(),
      DestinationDetail: () => ok({ destination: webdavDestination() }),
      DestinationsList: () => ok({ destinations: [] }),
      UpdateDestination: () => {
        attempt++;
        return attempt === 1
          ? gqlError("VALIDATION", "the server refused the URL", "webdav.url")
          : gqlError(
              "VALIDATION",
              "the URL, flavor or username changed: enter the password again",
              "webdav.password",
            );
      },
    });
    const username = await screen.findByLabelText("Username");
    // The stored password is write-only.
    expect(screen.getByText("Stored")).toBeInTheDocument();
    expect(screen.queryByLabelText("New password")).toBeNull();

    // Unchanged endpoint: no password is sent; a server error lands on its field.
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    expect(await screen.findByText("The server refused the URL.")).toBeInTheDocument();
    expect(screen.getByLabelText("URL")).toHaveAttribute("aria-invalid", "true");
    expect(callsOf("UpdateDestination")[0]?.variables).toEqual({
      id: DEST_ID,
      input: {
        webdav: {
          url: "https://cloud.example.com",
          flavor: "NEXTCLOUD",
          username: "bekup",
          path: "Backups",
        },
      },
    });

    // A server-side password error opens the write-only input to show it.
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    expect(
      await screen.findByText("The URL, flavor or username changed: enter the password again."),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("New password")).toHaveAttribute("aria-invalid", "true");

    await user.clear(username);
    await user.type(username, "backup-bot");
    expect(await screen.findByText(/The URL, server type or username changed/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    expect(
      await screen.findByText(/enter the password again\. A stored password/),
    ).toBeInTheDocument();
    expect(callsOf("UpdateDestination")).toHaveLength(2);
  });

  it("shows the TLS state and removes a pin behind a confirmation and a step-up", async () => {
    const state = { stepped: false };
    let cleared = false;
    const { user, callsOf } = renderApp(`/destinations/${DEST_ID}`, {
      ...baseHandlers(),
      ...stepUpHandlers(state),
      DestinationDetail: () =>
        ok({
          destination: cleared
            ? webdavDestination()
            : webdavDestination({
                trust: "PINNED",
                pinnedCertificate: webdavCert({ pinnedAt: "2026-10-20T08:00:00Z" }),
              }),
        }),
      DestinationUnrecordedFiles: noUnrecorded,
      ClearWebdavCertificatePin: () => {
        if (!state.stepped)
          return gqlError("STEP_UP_REQUIRED", "please confirm your identity again");
        cleared = true;
        return ok({
          clearWebdavCertificatePin: { __typename: "Destination", id: DEST_ID, ready: true },
        });
      },
    });
    const tls = await screen.findByRole("region", { name: "TLS" });
    expect(within(tls).getAllByText("Pinned certificate").length).toBeGreaterThan(0);
    const pinned = within(tls).getByTestId("pinned-certificate");
    expect(within(pinned).getByText(SPKI)).toBeInTheDocument();
    expect(within(pinned).getByText(/^FE:DC:BA:98/)).toBeInTheDocument();
    expect(within(pinned).getByText("Self-signed")).toBeInTheDocument();
    expect(within(pinned).getByText("Pinned")).toBeInTheDocument();
    const config = screen.getByRole("region", { name: "Configuration" });
    expect(within(config).getByText("https://cloud.example.com")).toBeInTheDocument();
    expect(within(config).getByText("Nextcloud / ownCloud")).toBeInTheDocument();
    expect(within(config).getByText("60 s (default)")).toBeInTheDocument();

    await user.click(within(tls).getByRole("button", { name: "Remove pin" }));
    const dialog = await screen.findByRole("dialog", { name: "Remove the certificate pin?" });
    expect(within(dialog).getByText(/system roots again/)).toBeInTheDocument();
    expect(within(dialog).getByText(/audited and raises a security alert/)).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Remove pin" }));
    const stepUp = await screen.findByRole("dialog", { name: "Confirm your identity" });
    await user.type(within(stepUp).getByLabelText("6-digit code"), "654321");
    await user.click(within(stepUp).getByRole("button", { name: "Confirm" }));
    await waitFor(() => {
      expect(callsOf("ClearWebdavCertificatePin")).toHaveLength(2);
    });
    expect(callsOf("ClearWebdavCertificatePin")[1]?.variables).toEqual({ id: DEST_ID });
    expect(await within(tls).findByText(/run the test above and pin/)).toBeInTheDocument();
  });

  it("shows the WebDAV test details and pins the presented certificate", async () => {
    const state = { stepped: false };
    const { user, callsOf } = renderApp(`/destinations/${DEST_ID}`, {
      ...baseHandlers(),
      ...stepUpHandlers(state),
      DestinationDetail: () => ok({ destination: webdavDestination({ flavor: "GENERIC" }) }),
      DestinationUnrecordedFiles: noUnrecorded,
      TestDestination: () => webdavTest(),
      PinWebdavCertificate: () =>
        state.stepped
          ? ok({ pinWebdavCertificate: { __typename: "Destination", id: DEST_ID, ready: true } })
          : gqlError("STEP_UP_REQUIRED", "please confirm your identity again"),
    });
    await user.click(await screen.findByRole("button", { name: "Test destination" }));
    const steps = await screen.findByRole("list", { name: "Test steps" });
    expect(within(steps).getByText("Overwrite protection")).toBeInTheDocument();
    expect(within(steps).getByText("overwrite_ignored")).toBeInTheDocument();

    const details = screen.getByTestId("webdav-test-details");
    expect(within(details).getByText("Not trusted")).toBeInTheDocument();
    expect(within(details).getByTestId("test-spki")).toHaveTextContent(SPKI);
    expect(within(details).getByText("Nextcloud")).toBeInTheDocument();
    expect(
      within(details).getByText(/This looks like Nextcloud: use the Nextcloud/),
    ).toBeInTheDocument();
    const ignored = within(details).getByTestId("overwrite-ignored");
    expect(ignored).toHaveTextContent(/This server would replace existing backups/);
    expect(ignored).toHaveTextContent(/ignored it and replaced the file/);
    expect(ignored).toHaveTextContent(/bekup refuses to write to it/);

    await user.click(within(details).getByRole("button", { name: "Pin this certificate" }));
    const dialog = await screen.findByRole("dialog", { name: "Pin this certificate" });
    expect(within(dialog).getByTestId("pin-spki")).toHaveTextContent(SPKI);
    expect(within(dialog).getByText(/^FE:DC:BA:98/)).toBeInTheDocument();
    const pin = within(dialog).getByRole("button", { name: "Pin certificate" });
    expect(pin).toBeDisabled();
    await user.click(within(dialog).getByLabelText(/I compared the fingerprint/));
    await user.click(pin);
    const stepUp = await screen.findByRole("dialog", { name: "Confirm your identity" });
    await user.type(within(stepUp).getByLabelText("6-digit code"), "654321");
    await user.click(within(stepUp).getByRole("button", { name: "Confirm" }));
    await waitFor(() => {
      expect(callsOf("PinWebdavCertificate")).toHaveLength(2);
    });
    expect(callsOf("PinWebdavCertificate")[1]?.variables).toEqual({
      id: DEST_ID,
      fingerprint: SPKI,
    });
    expect(await within(details).findByText(/Certificate pinned\. Test again/)).toBeInTheDocument();
    expect(within(details).queryByRole("button", { name: "Pin this certificate" })).toBeNull();
  });

  it("tells the admin to test again when the certificate changed (CONFLICT)", async () => {
    const { user } = renderApp(`/destinations/${DEST_ID}`, {
      ...baseHandlers(),
      ...stepUpHandlers({ stepped: true }),
      DestinationDetail: () => ok({ destination: webdavDestination() }),
      DestinationUnrecordedFiles: noUnrecorded,
      TestDestination: () =>
        webdavTest({
          trust: "PINNED",
          trusted: false,
          pinMatches: false,
          overwriteProtection: "REFUSED",
        }),
      PinWebdavCertificate: () => gqlError("CONFLICT", "the server presents another certificate"),
    });
    await user.click(await screen.findByRole("button", { name: "Test destination" }));
    const details = await screen.findByTestId("webdav-test-details");
    expect(within(details).getByText("Does not match the pin")).toBeInTheDocument();
    expect(
      within(details).getByText(/presents a different key than the pinned one/),
    ).toBeInTheDocument();
    expect(within(details).queryByTestId("overwrite-ignored")).toBeNull();
    // Same flavor as suggested: no hint.
    expect(within(details).queryByText(/This looks like/)).toBeNull();
    await user.click(within(details).getByRole("button", { name: "Pin this certificate" }));
    const dialog = await screen.findByRole("dialog", { name: "Pin this certificate" });
    await user.click(within(dialog).getByLabelText(/I compared the fingerprint/));
    await user.click(within(dialog).getByRole("button", { name: "Pin certificate" }));
    expect(
      await within(dialog).findByText(
        /now presents a different certificate.*test the destination again/,
      ),
    ).toBeInTheDocument();
  });

  it("shows maintainers the test details without pin actions", async () => {
    const { user } = renderApp(`/destinations/${DEST_ID}`, {
      ...baseHandlers(maintainer),
      DestinationDetail: () => ok({ destination: webdavDestination({}, false) }),
      TestDestination: () => webdavTest(),
    });
    expect(await screen.findByRole("heading", { name: "cloud-dav" })).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "TLS" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Test destination" }));
    const details = await screen.findByTestId("webdav-test-details");
    expect(within(details).getByTestId("test-spki")).toHaveTextContent(SPKI);
    expect(within(details).getByTestId("overwrite-ignored")).toBeInTheDocument();
    expect(within(details).queryByRole("button", { name: "Pin this certificate" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Remove pin" })).toBeNull();
  });

  it("form model: a stored password is only re-entered for a new endpoint", () => {
    const orig = webdavDestination() as unknown as Original;
    const v = valuesFrom(orig);
    expect(webdavNeedsPassword(v, orig)).toBe(false);
    expect(
      webdavNeedsPassword(
        { ...v, webdav: { ...v.webdav, url: "https://cloud.example.com/" } },
        orig,
      ),
    ).toBe(false);
    expect(webdavNeedsPassword({ ...v, webdav: { ...v.webdav, flavor: "GENERIC" } }, orig)).toBe(
      true,
    );
    expect(webdavNeedsPassword({ ...v, webdav: { ...v.webdav, path: "Other" } }, orig)).toBe(false);
  });
});
