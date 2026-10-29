import { screen, waitFor, within } from "@testing-library/react";
import { beforeAll, describe, expect, it } from "vitest";

import {
  DEST_ID,
  destinationFixture,
  stepUpHandlers,
  stubResizeObserver,
} from "@/features/connections/test-fixtures";
import { baseHandlers, gqlError, meFixture, ok, renderApp } from "@/test/mock-api";

import { sftpNeedsPassword, toUpdateInput, valuesFrom, type Original } from "./form-model";

beforeAll(stubResizeObserver);

const maintainer = meFixture({}, "MAINTAINER");

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
    // The stored secret access key is never displayed.
    expect(screen.queryByLabelText("New secret access key")).toBeNull();
    expect(screen.getByText("Stored")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    expect(await screen.findByText("The bucket does not exist.")).toBeInTheDocument();
    expect(screen.getByLabelText("Bucket")).toHaveAttribute("aria-invalid", "true");
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
