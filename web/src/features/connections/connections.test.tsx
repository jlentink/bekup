import { screen, waitFor, within } from "@testing-library/react";
import { beforeAll, describe, expect, it } from "vitest";

import { baseHandlers, gqlError, meFixture, ok, renderApp } from "@/test/mock-api";

import { jobsInUse } from "./delete-dialog-utils";
import { retarget, toUpdateInput, valuesFrom, type Original } from "./form-model";
import {
  chainFixture,
  CONN_ID,
  connectionFixture,
  connectionLastTest,
  engineVersionsHandler,
  sshSettings,
  stepUpHandlers,
  stubResizeObserver,
} from "./test-fixtures";

beforeAll(stubResizeObserver);

const maintainer = meFixture({}, "MAINTAINER");

function detail(c: Record<string, unknown>) {
  return () => ok({ connection: c });
}

describe("connections list", () => {
  it("shows transport, pin state and the run limit; admins get the create action", async () => {
    renderApp("/connections", {
      ...baseHandlers(),
      ConnectionsList: () =>
        ok({
          connections: [
            connectionFixture({
              pinStatus: "VALID",
              pinExpiresAt: "2027-01-01T00:00:00Z",
              ready: true,
            }),
            connectionFixture({
              id: "6f1c3e0a-0000-4000-8000-0000000000c2",
              name: "legacy-mysql",
              engine: "MYSQL",
              versionKey: "8.0",
              tlsMode: "UNVERIFIED",
              insecureTransport: true,
              pinStatus: "NOT_REQUIRED",
              sshEnabled: true,
              maxConcurrentRuns: 1,
              effectiveMaxConcurrentRuns: 1,
            }),
          ],
        }),
    });
    const table = await screen.findByRole("table");
    expect(within(table).getByRole("link", { name: "orders-db" })).toHaveAttribute(
      "href",
      `/connections/${CONN_ID}`,
    );
    expect(within(table).getByText("Pin valid")).toBeInTheDocument();
    expect(within(table).getByText("Insecure transport")).toBeInTheDocument();
    expect(within(table).getByText("Tunnel")).toBeInTheDocument();
    expect(within(table).getByText("set 1")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "New connection" })).toBeInTheDocument();
  });

  it("shows the stored last test and privilege verdict (D-187)", async () => {
    renderApp("/connections", {
      ...baseHandlers(),
      ConnectionsList: () =>
        ok({
          connections: [
            connectionFixture({
              lastTest: connectionLastTest({
                stale: true,
                privileges: {
                  __typename: "TargetPrivileges",
                  status: "PRIVILEGED",
                  reasons: ["superuser"],
                  acknowledgementRequired: true,
                },
              }),
            }),
            connectionFixture({
              id: "6f1c3e0a-0000-4000-8000-0000000000c2",
              name: "failing-db",
              lastTest: connectionLastTest({
                ok: false,
                code: "AUTH_FAILED",
                privileges: null,
              }),
            }),
            connectionFixture({ id: "6f1c3e0a-0000-4000-8000-0000000000c3", name: "fresh-db" }),
          ],
        }),
    });
    const table = await screen.findByRole("table");
    const rows = within(table).getAllByRole("row");
    const [, first, second, third] = rows;
    if (!first || !second || !third) throw new Error("rows missing");
    expect(within(first).getByText("Passed")).toBeInTheDocument();
    expect(within(first).getByText("Stale")).toBeInTheDocument();
    expect(within(first).getByText("Privileged role")).toBeInTheDocument();
    expect(within(first).getByText("from a stale test")).toBeInTheDocument();
    expect(within(second).getByText("Failed")).toBeInTheDocument();
    expect(within(second).getByText(/AUTH_FAILED/)).toBeInTheDocument();
    expect(within(second).getByText("Not checked")).toBeInTheDocument();
    expect(within(third).getAllByText("Not tested")).toHaveLength(2);
  });

  it("is read only for maintainers", async () => {
    renderApp("/connections", {
      ...baseHandlers(maintainer),
      ConnectionsList: () => ok({ connections: [connectionFixture({}, false)] }),
    });
    expect(await screen.findByRole("link", { name: "orders-db" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "New connection" })).toBeNull();
    expect(screen.getByText(/Read only: admins manage connections/)).toBeInTheDocument();
  });

  it("guides through an empty list", async () => {
    renderApp("/connections", {
      ...baseHandlers(),
      ConnectionsList: () => ok({ connections: [] }),
    });
    expect(await screen.findByText("No connections yet")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Add the first connection" })).toBeInTheDocument();
  });
});

describe("connection detail", () => {
  it("lets maintainers run checks but hides every admin action", async () => {
    const { user, callsOf } = renderApp(`/connections/${CONN_ID}`, {
      ...baseHandlers(maintainer),
      ConnectionDetail: detail(connectionFixture({ sshEnabled: true }, false)),
      TestConnection: () =>
        ok({
          testConnection: {
            ok: true,
            code: null,
            message: null,
            serverVersion: "17.2",
            detectedVersionKey: "17",
            compatible: true,
            legacy: false,
            warnings: ["tls_certificate_expires_within_30_days"],
            databaseCount: 3,
            privileges: {
              status: "PRIVILEGED",
              reasons: ["superuser", "member_of:pg_execute_server_program"],
              acknowledgementRequired: true,
            },
          },
        }),
    });
    expect(await screen.findByRole("heading", { name: "orders-db" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Edit" })).toBeNull();
    expect(screen.queryByRole("button", { name: /Pin certificate/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /Change TLS mode/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /host key/i })).toBeNull();
    expect(screen.queryByRole("button", { name: /Delete connection/ })).toBeNull();
    expect(screen.getByText(/visible to admins only/)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Test connection" }));
    expect(await screen.findByText("Connection works")).toBeInTheDocument();
    expect(
      screen.getByText("The server's certificate chain expires within 30 days."),
    ).toBeInTheDocument();
    expect(screen.getByText("Privileged role")).toBeInTheDocument();
    expect(screen.getByText("Superuser")).toBeInTheDocument();
    expect(screen.getByText("Member of pg_execute_server_program")).toBeInTheDocument();
    expect(callsOf("TestConnection")).toHaveLength(1);
  });

  it("shows the stored last test until a new test runs, then refetches it", async () => {
    const { user, callsOf } = renderApp(`/connections/${CONN_ID}`, {
      ...baseHandlers(maintainer),
      ConnectionDetail: detail(
        connectionFixture(
          {
            lastTest: connectionLastTest({
              ok: false,
              code: "TLS_PIN_MISMATCH",
              stale: true,
              privileges: {
                __typename: "TargetPrivileges",
                status: "UNKNOWN",
                reasons: ["roles_granted"],
                acknowledgementRequired: true,
              },
            }),
          },
          false,
        ),
      ),
      TestConnection: () =>
        ok({
          testConnection: {
            ok: true,
            code: null,
            message: null,
            serverVersion: "17.2",
            detectedVersionKey: "17",
            compatible: true,
            legacy: false,
            warnings: [],
            databaseCount: 1,
            privileges: null,
          },
        }),
    });
    const stored = await screen.findByTestId("stored-test");
    expect(within(stored).getByText("Failed")).toBeInTheDocument();
    expect(within(stored).getByText("TLS_PIN_MISMATCH")).toBeInTheDocument();
    expect(within(stored).getByText(/Stale: the settings changed/)).toBeInTheDocument();
    expect(within(stored).getByText("Privileges unknown")).toBeInTheDocument();
    expect(within(stored).getByText("Roles are granted (not inspected)")).toBeInTheDocument();
    expect(callsOf("ConnectionDetail")).toHaveLength(1);

    await user.click(screen.getByRole("button", { name: "Test connection" }));
    expect(await screen.findByText("Connection works")).toBeInTheDocument();
    expect(screen.queryByTestId("stored-test")).toBeNull();
    await waitFor(() => {
      expect(callsOf("ConnectionDetail")).toHaveLength(2);
    });
  });

  it("pins a captured certificate after comparison and a step-up", async () => {
    const state = { stepped: false };
    const { user, callsOf } = renderApp(`/connections/${CONN_ID}`, {
      ...baseHandlers(),
      ...stepUpHandlers(state),
      ConnectionDetail: detail(connectionFixture()),
      FetchConnectionCertificate: () => ok({ fetchCertificate: chainFixture() }),
      ConfirmConnectionCertificatePin: () =>
        state.stepped
          ? ok({
              confirmCertificatePin: {
                __typename: "Connection",
                id: CONN_ID,
                pinStatus: "VALID",
                pinExpiresAt: "2030-01-01T00:00:00Z",
              },
            })
          : gqlError("STEP_UP_REQUIRED", "please confirm your identity again"),
    });
    expect((await screen.findAllByText("Pin missing")).length).toBeGreaterThan(0);
    await user.click(screen.getByRole("button", { name: "Pin certificate" }));
    const dialog = await screen.findByRole("dialog", { name: "Pin the server certificate" });
    expect(within(dialog).getByText("Why a public CA is refused")).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Fetch certificate" }));

    const root = await within(dialog).findByTestId("chain-cert-1");
    expect(within(root).getByText("Suggested anchor")).toBeInTheDocument();
    expect(within(root).getByRole("radio", { name: "Root CA" })).toBeChecked();
    expect(within(dialog).getByTestId("chain-cert-0")).toHaveTextContent("db.internal.example");
    // The leaf cannot be an anchor (not self-signed).
    expect(within(within(dialog).getByTestId("chain-cert-0")).queryByRole("radio")).toBeNull();

    const confirm = within(dialog).getByRole("button", { name: "Confirm pin" });
    expect(confirm).toBeDisabled();
    await user.click(within(dialog).getByLabelText(/I compared the fingerprints/));
    await user.click(confirm);

    const stepUp = await screen.findByRole("dialog", { name: "Confirm your identity" });
    await user.type(within(stepUp).getByLabelText("6-digit code"), "654321");
    await user.click(within(stepUp).getByRole("button", { name: "Confirm" }));

    await waitFor(() => {
      expect(callsOf("ConfirmConnectionCertificatePin")).toHaveLength(2);
    });
    expect(callsOf("ConfirmConnectionCertificatePin")[1]?.variables).toEqual({
      id: CONN_ID,
      input: { certIndex: 1, leafSpkiSha256: chainFixture().leafSpkiSha256 },
    });
    await waitFor(() => {
      expect(screen.queryByRole("dialog", { name: "Pin the server certificate" })).toBeNull();
    });
  });

  it("confirms the SSH host key bound to the endpoint it was fetched from", async () => {
    const state = { stepped: true };
    const { user, callsOf } = renderApp(`/connections/${CONN_ID}`, {
      ...baseHandlers(),
      ...stepUpHandlers(state),
      ConnectionDetail: detail(
        connectionFixture({
          sshEnabled: true,
          settings: { ...connectionFixture().settings, ssh: sshSettings() },
        }),
      ),
      FetchConnectionSshHostKey: () =>
        ok({
          fetchSshHostKey: {
            __typename: "SshHostKey",
            type: "ssh-ed25519",
            fingerprintSha256: "SHA256:3b6YJ9d0bastionfingerprintxx",
            authorizedKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIbastion",
            confirmedAt: null,
            host: "bastion.example",
            port: 2222,
          },
        }),
      ConfirmConnectionSshHostKey: () =>
        ok({ confirmSshHostKey: { __typename: "Connection", id: CONN_ID, ready: true } }),
    });
    await user.click(await screen.findByRole("button", { name: "Fetch and confirm host key" }));
    const dialog = await screen.findByRole("dialog", { name: "Confirm the host key" });
    await user.click(within(dialog).getByRole("button", { name: "Fetch host key" }));
    expect(await within(dialog).findByTestId("host-key-fingerprint")).toHaveTextContent(
      "SHA256:3b6YJ9d0bastionfingerprintxx",
    );
    expect(
      within(dialog).getByText("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIbastion"),
    ).toBeInTheDocument();
    const pin = within(dialog).getByRole("button", { name: "Pin this host key" });
    expect(pin).toBeDisabled();
    await user.click(within(dialog).getByLabelText(/I compared this fingerprint/));
    await user.click(pin);
    await waitFor(() => {
      expect(callsOf("ConfirmConnectionSshHostKey")).toHaveLength(1);
    });
    expect(callsOf("ConfirmConnectionSshHostKey")[0]?.variables).toEqual({
      id: CONN_ID,
      authorizedKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIbastion",
      expectedHost: "bastion.example",
      expectedPort: 2222,
    });
  });

  it("asks for the connection name before weakening TLS", async () => {
    const { user, callsOf } = renderApp(`/connections/${CONN_ID}`, {
      ...baseHandlers(),
      ConnectionDetail: detail(connectionFixture()),
      SetConnectionTlsMode: () =>
        ok({
          setTlsMode: {
            __typename: "Connection",
            id: CONN_ID,
            tlsMode: "UNVERIFIED",
            insecureTransport: true,
            pinStatus: "NOT_REQUIRED",
          },
        }),
    });
    await user.click(await screen.findByRole("button", { name: "Change TLS mode" }));
    const dialog = await screen.findByRole("dialog", { name: "Change the TLS mode" });
    await user.click(within(dialog).getByLabelText("TLS without verification"));
    expect(within(dialog).getByText("This weakens the connection's security")).toBeInTheDocument();
    const submit = within(dialog).getByRole("button", { name: "Weaken TLS" });
    expect(submit).toBeDisabled();
    await user.type(within(dialog).getByLabelText(/Type the connection name/), "orders-db");
    await user.click(submit);
    await waitFor(() => {
      expect(callsOf("SetConnectionTlsMode")[0]?.variables).toEqual({
        id: CONN_ID,
        mode: "UNVERIFIED",
      });
    });
  });

  it("lists the jobs that block a delete", async () => {
    const { user } = renderApp(`/connections/${CONN_ID}`, {
      ...baseHandlers(),
      ConnectionDetail: detail(connectionFixture()),
      DeleteConnection: () =>
        gqlError("CONFLICT", "the connection is still used by these jobs: nightly, weekly"),
    });
    await user.click(await screen.findByRole("button", { name: "Delete connection" }));
    const dialog = await screen.findByRole("dialog", { name: /Delete the connection/ });
    await user.click(within(dialog).getByRole("button", { name: "Delete connection" }));
    const jobs = await within(dialog).findByRole("list", { name: "Jobs using it" });
    expect(within(jobs).getByText("nightly")).toBeInTheDocument();
    expect(within(jobs).getByText("weekly")).toBeInTheDocument();
  });
});

describe("connection form", () => {
  it("creates a connection with write-only credentials and maps server field errors", async () => {
    let attempt = 0;
    const { user, callsOf, router } = renderApp("/connections/new", {
      ...baseHandlers(),
      ConnectionEngineVersions: engineVersionsHandler,
      ConnectionsList: () => ok({ connections: [] }),
      ConnectionDetail: detail(connectionFixture()),
      CreateConnection: () => {
        attempt++;
        return attempt === 1
          ? gqlError("VALIDATION", "pinned TLS to an IP address needs an SSH tunnel", "host")
          : ok({ createConnection: { __typename: "Connection", id: CONN_ID, name: "orders-db" } });
      },
    });
    await user.type(await screen.findByLabelText("Name"), "orders-db");
    await user.selectOptions(screen.getByLabelText("Server version"), "17");
    await user.type(screen.getByLabelText("Host"), "10.0.0.5");
    await user.type(screen.getByLabelText("User"), "backup");
    await user.type(screen.getByLabelText("Password"), "hunter2-correct-horse");
    await user.click(screen.getByRole("button", { name: "Create connection" }));

    const host = screen.getByLabelText("Host");
    expect(
      await screen.findByText("Pinned TLS to an IP address needs an SSH tunnel."),
    ).toBeInTheDocument();
    expect(host).toHaveAttribute("aria-invalid", "true");

    await user.clear(host);
    await user.type(host, "db.internal.example");
    await user.click(screen.getByRole("button", { name: "Create connection" }));
    await waitFor(() => {
      expect(router.state.location.pathname).toBe(`/connections/${CONN_ID}`);
    });
    expect(callsOf("CreateConnection")[1]?.variables).toEqual({
      input: {
        name: "orders-db",
        engine: "POSTGRES",
        versionKey: "17",
        host: "db.internal.example",
        username: "backup",
        password: "hunter2-correct-horse",
        tlsMode: "PINNED",
      },
    });
    // The password never appears on the page afterwards.
    await screen.findByRole("heading", { name: "orders-db" });
    expect(document.body.textContent).not.toContain("hunter2-correct-horse");
  });

  it("never shows a stored password and demands it again after a host change (D-140)", async () => {
    const { user, callsOf } = renderApp(`/connections/${CONN_ID}/edit`, {
      ...baseHandlers(),
      ConnectionEngineVersions: engineVersionsHandler,
      ConnectionDetail: detail(connectionFixture()),
      ConnectionsList: () => ok({ connections: [] }),
      UpdateConnection: () =>
        ok({ updateConnection: { __typename: "Connection", id: CONN_ID, name: "orders-db" } }),
    });
    const host = await screen.findByLabelText("Host");
    expect(screen.getByText("Stored")).toBeInTheDocument();
    for (const input of document.querySelectorAll("input")) {
      expect(input.type === "password" ? input.value : "").toBe("");
    }
    expect(screen.queryByLabelText("New password")).toBeNull();

    await user.clear(host);
    await user.type(host, "db2.internal.example");
    expect(await screen.findByText(/The database endpoint changed/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    expect(await screen.findByText(/enter the password again, or remove it/)).toBeInTheDocument();
    expect(callsOf("UpdateConnection")).toHaveLength(0);

    await user.type(screen.getByLabelText("New password"), "new-secret-value-1");
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => {
      expect(callsOf("UpdateConnection")).toHaveLength(1);
    });
    expect(callsOf("UpdateConnection")[0]?.variables).toEqual({
      id: CONN_ID,
      input: { host: "db2.internal.example", password: "new-secret-value-1" },
    });
  });
});

describe("form model", () => {
  const orig = connectionFixture({
    sshEnabled: true,
    settings: { ...connectionFixture().settings, ssh: sshSettings() },
  }) as unknown as Original;

  it("requires re-entry only when an endpoint moves", () => {
    const v = valuesFrom(orig);
    expect(retarget(v, orig)).toMatchObject({ needsPassword: false, needsSshPassword: false });
    expect(retarget({ ...v, ssh: { ...v.ssh, user: "other" } }, orig)).toMatchObject({
      needsPassword: false,
      needsSshPassword: true,
    });
    expect(retarget({ ...v, port: "6543" }, orig).needsPassword).toBe(true);
    expect(retarget({ ...v, sshEnabled: false }, orig).needsPassword).toBe(true);
  });

  it("sends only changed fields and the tunnel removal", () => {
    const v = valuesFrom(orig);
    expect(toUpdateInput(v, orig)).toEqual({});
    expect(toUpdateInput({ ...v, maxConcurrentRuns: "4", name: "x" }, orig)).toEqual({
      name: "x",
      maxConcurrentRuns: 4,
    });
    expect(toUpdateInput({ ...v, sshEnabled: false, passwordAction: "clear" }, orig)).toEqual({
      clearPassword: true,
      disableSsh: true,
    });
  });

  it("reads the jobs out of an in-use conflict", () => {
    expect(jobsInUse("the connection is still used by these jobs: a, b c")).toEqual(["a", "b c"]);
    expect(jobsInUse("the connection is still used by a job")).toEqual([]);
  });
});
