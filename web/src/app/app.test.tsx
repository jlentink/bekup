import { getOperationAST } from "graphql";
import { screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  baseHandlers,
  gqlError,
  type Handler,
  meFixture,
  ok,
  renderApp,
  serverInfoFixture,
} from "@/test/mock-api";
import { bytes, stubWebAuthn } from "@/test/webauthn";

let restore: (() => void) | undefined;

afterEach(() => {
  restore?.();
  restore = undefined;
  vi.unstubAllGlobals();
  window.sessionStorage.clear();
});

const signedOut: Handler = () => gqlError("UNAUTHENTICATED", "authentication required");

/** A Me handler that answers UNAUTHENTICATED until signIn() is called. */
function sessionState(me = meFixture()) {
  let signedIn = false;
  return {
    handler: (() => (signedIn ? ok(me) : signedOut({}))) as Handler,
    signIn: () => {
      signedIn = true;
    },
  };
}

describe("session gate", () => {
  it("sends a signed-out visitor to login with the page as return path", async () => {
    const { router } = renderApp("/jobs?x=1", { ...baseHandlers(), Me: signedOut });
    expect(await screen.findByRole("heading", { name: "Welcome back" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/login");
    expect(router.state.location.search).toBe("?next=%2Fjobs%3Fx%3D1");
  });

  it("sends a login waiting for its second factor to the verify page", async () => {
    const { router } = renderApp("/", {
      ...baseHandlers(),
      Me: () => gqlError("SECOND_FACTOR_REQUIRED"),
    });
    expect(await screen.findByRole("heading", { name: "Confirm it is you" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/login/verify");
  });
});

describe("login", () => {
  it("signs in with password, then TOTP, and returns to the requested page", async () => {
    const session = sessionState();
    const { user, router, callsOf } = renderApp("/login?next=%2Fjobs", {
      ...baseHandlers(),
      Me: session.handler,
      Login: () =>
        ok({
          login: {
            secondFactorRequired: true,
            availableFactors: ["TOTP", "RECOVERY_CODE"],
            passwordChangeRequired: false,
            twoFactorEnrollmentRequired: false,
            user: null,
          },
        }),
      CompleteSecondFactor: (vars) => {
        if (vars["code"] !== "123456")
          return gqlError("VALIDATION", "the code is not valid", "code");
        session.signIn();
        return ok({
          completeSecondFactor: {
            secondFactorRequired: false,
            passwordChangeRequired: false,
            twoFactorEnrollmentRequired: false,
            user: { __typename: "User", id: meFixture().me.id },
          },
        });
      },
    });

    await user.type(await screen.findByLabelText("E-mail address"), "ada@example.com");
    await user.type(screen.getByLabelText("Password"), "correct horse battery staple");
    await user.click(screen.getByRole("button", { name: "Sign in" }));

    expect(await screen.findByRole("heading", { name: "Confirm it is you" })).toBeInTheDocument();
    expect(callsOf("Login")[0]?.variables).toEqual({
      email: "ada@example.com",
      password: "correct horse battery staple",
    });
    // Only the factors of this account are offered (no passkey).
    expect(screen.queryByRole("radio", { name: "Passkey" })).toBeNull();
    expect(screen.getByRole("radio", { name: "Recovery code" })).toBeInTheDocument();

    const code = screen.getByLabelText("6-digit code");
    await user.type(code, "000000");
    await user.click(screen.getByRole("button", { name: "Verify" }));
    expect(await screen.findByText("The code is not valid.")).toBeInTheDocument();

    await user.clear(code);
    await user.type(code, "123 456");
    await user.click(screen.getByRole("button", { name: "Verify" }));

    expect(await screen.findByRole("heading", { name: "Jobs" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/jobs");
    expect(callsOf("CompleteSecondFactor").at(-1)?.variables).toEqual({
      method: "TOTP",
      code: "123456",
    });
  });

  it("completes the second step with a passkey", async () => {
    const get = vi.fn().mockResolvedValue({
      id: "AQI",
      type: "public-key",
      rawId: bytes(1, 2),
      authenticatorAttachment: "platform",
      getClientExtensionResults: () => ({}),
      response: {
        clientDataJSON: bytes(3),
        authenticatorData: bytes(4),
        signature: bytes(5),
        userHandle: null,
      },
    });
    restore = stubWebAuthn({ get, create: vi.fn() });
    window.sessionStorage.setItem("bekup.pendingFactors", JSON.stringify(["PASSKEY", "TOTP"]));

    const session = sessionState();
    const { user, router, callsOf } = renderApp("/login/verify", {
      ...baseHandlers(),
      Me: session.handler,
      BeginPasskeyLogin: () =>
        ok({
          beginPasskeyLogin: {
            publicKey: {
              challenge: "AAEC",
              rpId: "localhost",
              allowCredentials: [{ type: "public-key", id: "AQI" }],
              userVerification: "preferred",
            },
          },
        }),
      CompleteSecondFactor: () => {
        session.signIn();
        return ok({
          completeSecondFactor: {
            secondFactorRequired: false,
            passwordChangeRequired: false,
            twoFactorEnrollmentRequired: false,
            user: { __typename: "User", id: meFixture().me.id },
          },
        });
      },
    });

    await user.click(await screen.findByRole("button", { name: "Use a passkey" }));
    await waitFor(() => {
      expect(router.state.location.pathname).toBe("/");
    });
    const options = get.mock.calls[0]?.[0] as { publicKey: PublicKeyCredentialRequestOptions };
    expect(new Uint8Array(options.publicKey.challenge as ArrayBuffer)).toEqual(
      new Uint8Array([0, 1, 2]),
    );
    expect(callsOf("CompleteSecondFactor")[0]?.variables).toEqual({
      method: "PASSKEY",
      assertion: {
        id: "AQI",
        rawId: "AQI",
        type: "public-key",
        authenticatorAttachment: "platform",
        clientExtensionResults: {},
        response: { clientDataJSON: "Aw", authenticatorData: "BA", signature: "BQ" },
      },
    });
    expect(window.sessionStorage.getItem("bekup.pendingFactors")).toBeNull();
  });

  it("answers every failed login with the same generic message", async () => {
    const { user } = renderApp("/login", {
      ...baseHandlers(),
      Me: signedOut,
      Login: () => gqlError("UNAUTHENTICATED", "invalid email or password"),
    });
    await user.type(await screen.findByLabelText("E-mail address"), "nobody@example.com");
    await user.type(screen.getByLabelText("Password"), "whatever-it-is");
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    expect(
      await screen.findByText("The e-mail address or password is not correct."),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Password")).toHaveValue("");
  });

  it("shows the lockout message with its wait time", async () => {
    const { user } = renderApp("/login", {
      ...baseHandlers(),
      Me: signedOut,
      Login: () => gqlError("RATE_LIMITED", "too many login attempts; try again in 60 seconds"),
    });
    await user.type(await screen.findByLabelText("E-mail address"), "ada@example.com");
    await user.type(screen.getByLabelText("Password"), "x");
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    expect(
      await screen.findByText("Too many login attempts; try again in 60 seconds."),
    ).toBeInTheDocument();
  });

  it("validates the form before calling the server", async () => {
    const { user, callsOf } = renderApp("/login", { ...baseHandlers(), Me: signedOut });
    await user.click(await screen.findByRole("button", { name: "Sign in" }));
    expect(await screen.findByText("Enter your e-mail address.")).toBeInTheDocument();
    expect(screen.getByText("Enter your password.")).toBeInTheDocument();
    expect(callsOf("Login")).toHaveLength(0);
  });
});

describe("forced flows", () => {
  it("requires a temporary password to be changed before anything else", async () => {
    let changed = false;
    const { user, callsOf } = renderApp("/jobs", {
      ...baseHandlers(),
      Me: () => ok(meFixture({ passwordChangeRequired: !changed })),
      ChangePassword: (vars) => {
        if (vars["currentPassword"] !== "temp-password-123") {
          return gqlError("VALIDATION", "the current password is wrong", "currentPassword");
        }
        changed = true;
        return ok({ changePassword: true });
      },
    });
    expect(
      await screen.findByRole("heading", { name: "Choose your own password" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("navigation", { name: "Main" })).toBeNull();

    await user.type(screen.getByLabelText("Temporary password"), "wrong");
    await user.type(screen.getByLabelText("New password"), "violet-anchor-meadow-41");
    await user.type(screen.getByLabelText("Repeat the new password"), "violet-anchor-meadow-41");
    await user.click(screen.getByRole("button", { name: "Change password" }));
    expect(await screen.findByText("The current password is wrong.")).toBeInTheDocument();

    await user.clear(screen.getByLabelText("Temporary password"));
    await user.type(screen.getByLabelText("Temporary password"), "temp-password-123");
    await user.click(screen.getByRole("button", { name: "Change password" }));

    expect(await screen.findByRole("heading", { name: "Jobs" })).toBeInTheDocument();
    expect(callsOf("ChangePassword")).toHaveLength(2);
  });

  it("blocks the app once 2FA enrollment is overdue", async () => {
    renderApp("/", {
      ...baseHandlers(
        meFixture({ twoFactorRequired: true, twoFactorEnrollmentDeadline: "2020-01-01T00:00:00Z" }),
      ),
      TwoFactorOverview: () =>
        ok({
          myTwoFactorStatus: {
            __typename: "TwoFactorStatus",
            totpEnabled: false,
            totpEnabledAt: null,
            totpEnrollmentPending: false,
            recoveryCodesRemaining: 0,
          },
          webauthnStatus: {
            __typename: "WebAuthnStatus",
            webauthnAvailable: false,
            reason: "server.base_url is plain http",
            rpId: null,
          },
          myPasskeys: [],
        }),
    });
    expect(await screen.findByRole("heading", { name: "Add a second factor" })).toBeInTheDocument();
    expect(
      await screen.findByRole("button", { name: "Set up an authenticator app" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Passkeys cannot be used with this server.")).toBeInTheDocument();
    expect(screen.queryByRole("navigation", { name: "Main" })).toBeNull();
  });

  it("shows the deadline banner during the grace period", async () => {
    const deadline = new Date(Date.now() + 3 * 86_400_000).toISOString();
    renderApp("/", {
      ...baseHandlers(
        meFixture({ twoFactorRequired: true, twoFactorEnrollmentDeadline: deadline }),
      ),
    });
    const banner = await screen.findByText(/Two-factor authentication is required for your role/);
    expect(banner).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Set up now" })).toHaveAttribute(
      "href",
      "/profile/security",
    );
    expect(screen.getByRole("navigation", { name: "Main" })).toBeInTheDocument();
  });
});

describe("shell", () => {
  it.each([
    ["ADMIN", ["Users", "Connections", "Jobs"], []],
    ["MAINTAINER", ["Connections", "Jobs"], ["Users"]],
    ["USER", ["Jobs", "Calendar"], ["Connections", "Destinations", "Users"]],
  ] as const)("shows the %s navigation", async (role, visible, hidden) => {
    renderApp("/", baseHandlers(meFixture({}, role)));
    const nav = await screen.findByRole("navigation", { name: "Main" });
    for (const label of visible)
      expect(within(nav).getByRole("link", { name: label })).toBeInTheDocument();
    for (const label of hidden) expect(within(nav).queryByRole("link", { name: label })).toBeNull();
  });

  it.each([
    ["USER", "/connections", "Admins and maintainers only"],
    ["USER", "/destinations/new", "Admins and maintainers only"],
    ["USER", "/jobs/new", "Admins and maintainers only"],
    ["MAINTAINER", "/admin/settings", "Admins only"],
    ["MAINTAINER", "/admin/users", "Admins only"],
  ] as const)("guards %s from %s", async (role, path, title) => {
    const { calls } = renderApp(path, baseHandlers(meFixture({}, role)));
    expect(await screen.findByRole("heading", { name: title })).toBeInTheDocument();
    // The page's own queries never run.
    const pageQueries = new Set([
      "ConnectionsList",
      "DestinationsList",
      "JobEditorOptions",
      "AdminSmtpSettings",
      "AdminUsers",
    ]);
    expect(calls.filter((c) => pageQueries.has(c.name))).toEqual([]);
  });

  it("shows the red insecure transport banner when the server reports it", async () => {
    vi.stubGlobal("location", {
      protocol: "https:",
      origin: "https://localhost",
      host: "localhost",
    });
    renderApp("/", { ...baseHandlers(), ServerInfo: () => ok(serverInfoFixture(true)) });
    expect(await screen.findByText(/Connection is not encrypted \(no HTTPS\)/)).toBeInTheDocument();
  });

  it("goes to login with a return path when any request is unauthenticated", async () => {
    let ended = false;
    const { router } = renderApp("/profile/sessions", {
      ...baseHandlers(),
      Me: () => (ended ? signedOut({}) : ok(meFixture())),
      MySessions: () => {
        ended = true;
        return gqlError("UNAUTHENTICATED", "authentication required");
      },
    });
    await waitFor(() => {
      expect(router.state.location.pathname).toBe("/login");
    });
    expect(router.state.location.search).toBe("?next=%2Fprofile%2Fsessions");
    expect(
      await screen.findByText("Your session ended. Sign in again to continue."),
    ).toBeInTheDocument();
  });

  it("signs out from the account menu", async () => {
    const { user, router, callsOf } = renderApp("/", {
      ...baseHandlers(),
      Logout: () => ok({ logout: true }),
    });
    await user.click(await screen.findByRole("button", { name: "Account menu" }));
    await user.click(await screen.findByRole("menuitem", { name: "Sign out" }));
    await waitFor(() => {
      expect(router.state.location.pathname).toBe("/login");
    });
    expect(callsOf("Logout")).toHaveLength(1);
  });

  it("stays signed in and says so when the server refuses the logout", async () => {
    const { user, router, callsOf } = renderApp("/", {
      ...baseHandlers(),
      Logout: () => gqlError("FORBIDDEN", "not allowed"),
    });
    await user.click(await screen.findByRole("button", { name: "Account menu" }));
    await user.click(await screen.findByRole("menuitem", { name: "Sign out" }));
    expect(
      await screen.findByText("Could not sign out. You are still signed in; try again."),
    ).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/");
    expect(callsOf("Logout")).toHaveLength(1);
  });

  it("treats an already ended session as signed out", async () => {
    const { user, router } = renderApp("/", {
      ...baseHandlers(),
      Logout: () => gqlError("UNAUTHENTICATED", "authentication required"),
    });
    await user.click(await screen.findByRole("button", { name: "Account menu" }));
    await user.click(await screen.findByRole("menuitem", { name: "Sign out" }));
    await waitFor(() => {
      expect(router.state.location.pathname).toBe("/login");
    });
  });
});

describe("second-factor material", () => {
  it("never writes the TOTP key or recovery codes to the Apollo cache", async () => {
    const KEY = "JBSWY3DPEHPK3PXPJBSWY3DP";
    const CODE = "zzzz-yyyy-xxxx";
    const { user, client, callsOf } = renderApp("/profile/security", {
      ...baseHandlers(),
      TwoFactorOverview: () =>
        ok({
          myTwoFactorStatus: {
            __typename: "TwoFactorStatus",
            totpEnabled: false,
            totpEnabledAt: null,
            totpEnrollmentPending: false,
            recoveryCodesRemaining: 0,
          },
          webauthnStatus: {
            __typename: "WebAuthnStatus",
            webauthnAvailable: false,
            reason: "server.base_url is plain http",
            rpId: null,
          },
          myPasskeys: [],
        }),
      BeginTotpEnrollment: () =>
        ok({
          beginTotpEnrollment: {
            __typename: "TotpEnrollment",
            qrCodeSvg: "<svg></svg>",
            manualEntryKey: KEY,
            expiresAt: "2026-10-28T12:10:00Z",
          },
        }),
      ConfirmTotpEnrollment: () =>
        ok({
          confirmTotpEnrollment: { __typename: "RecoveryCodes", codes: [CODE] },
        }),
    });
    const mutate = vi.spyOn(client, "mutate");
    const policyOf = (name: string) =>
      mutate.mock.calls
        .filter(([o]) => getOperationAST(o.mutation)?.name?.value === name)
        .map(([o]) => o.fetchPolicy);
    await user.click(await screen.findByRole("button", { name: "Set up an authenticator app" }));
    expect(await screen.findByText(/JBSW Y3DP/)).toBeInTheDocument();
    expect(policyOf("BeginTotpEnrollment")).toEqual(["no-cache"]);
    expect(JSON.stringify(client.extract())).not.toContain(KEY);
    await user.type(screen.getByLabelText("Code from the app"), "123456");
    await user.click(screen.getByRole("button", { name: "Confirm and turn on" }));
    expect(await screen.findByText(CODE)).toBeInTheDocument();
    expect(callsOf("ConfirmTotpEnrollment")).toHaveLength(1);
    expect(policyOf("ConfirmTotpEnrollment")).toEqual(["no-cache"]);
    expect(JSON.stringify(client.extract())).not.toContain(CODE);
  });

  it("regenerates recovery codes without caching them", async () => {
    const CODE = "qqqq-rrrr-ssss";
    const { user, client } = renderApp("/profile/security", {
      ...baseHandlers(),
      TwoFactorOverview: () =>
        ok({
          myTwoFactorStatus: {
            __typename: "TwoFactorStatus",
            totpEnabled: true,
            totpEnabledAt: "2026-10-01T00:00:00Z",
            totpEnrollmentPending: false,
            recoveryCodesRemaining: 3,
          },
          webauthnStatus: {
            __typename: "WebAuthnStatus",
            webauthnAvailable: false,
            reason: "server.base_url is plain http",
            rpId: null,
          },
          myPasskeys: [],
        }),
      RegenerateRecoveryCodes: () =>
        ok({ regenerateRecoveryCodes: { __typename: "RecoveryCodes", codes: [CODE] } }),
    });
    const mutate = vi.spyOn(client, "mutate");
    await user.click(await screen.findByRole("button", { name: "Create new codes" }));
    const dialog = await screen.findByRole("dialog", { name: "Create new recovery codes?" });
    await user.click(within(dialog).getByRole("button", { name: "Create new codes" }));
    expect(await screen.findByText(CODE)).toBeInTheDocument();
    const policies = mutate.mock.calls
      .filter(([o]) => getOperationAST(o.mutation)?.name?.value === "RegenerateRecoveryCodes")
      .map(([o]) => o.fetchPolicy);
    expect(policies).toEqual(["no-cache"]);
  });
});

describe("step-up", () => {
  it("asks for a fresh factor and retries the operation", async () => {
    let stepped = false;
    const { user, callsOf } = renderApp("/profile", {
      ...baseHandlers(),
      ChangePassword: () =>
        stepped
          ? ok({ changePassword: true })
          : gqlError("STEP_UP_REQUIRED", "please confirm your identity again"),
      BeginStepUp: () =>
        ok({
          beginStepUp: {
            __typename: "StepUpChallenge",
            methods: ["TOTP", "RECOVERY_CODE"],
            passkeyOptions: null,
          },
        }),
      CompleteStepUp: (vars) => {
        stepped = vars["method"] === "TOTP" && vars["code"] === "654321";
        return stepped
          ? ok({
              completeStepUp: {
                __typename: "StepUpResult",
                stepUpAt: "2026-10-28T10:00:00Z",
                aal2At: "2026-10-28T10:00:00Z",
                aal: 2,
              },
            })
          : gqlError("VALIDATION", "the code is not valid", "code");
      },
    });

    await user.type(await screen.findByLabelText("Current password"), "old-password-1");
    await user.type(screen.getByLabelText("New password"), "violet-anchor-meadow-41");
    await user.type(screen.getByLabelText("Repeat the new password"), "violet-anchor-meadow-41");
    await user.click(screen.getByRole("button", { name: "Change password" }));

    const dialog = await screen.findByRole("dialog", { name: "Confirm your identity" });
    await user.type(within(dialog).getByLabelText("6-digit code"), "654321");
    await user.click(within(dialog).getByRole("button", { name: "Confirm" }));

    expect(
      await screen.findByText("Password changed. Your other sessions were signed out."),
    ).toBeInTheDocument();
    expect(callsOf("ChangePassword")).toHaveLength(2);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("offers the password to accounts without a second factor and stops on cancel", async () => {
    const { user, callsOf } = renderApp("/profile", {
      ...baseHandlers(),
      ChangePassword: () => gqlError("STEP_UP_REQUIRED", "please confirm your identity again"),
      BeginStepUp: () =>
        ok({
          beginStepUp: {
            __typename: "StepUpChallenge",
            methods: ["PASSWORD"],
            passkeyOptions: null,
          },
        }),
    });
    await user.type(await screen.findByLabelText("Current password"), "old-password-1");
    await user.type(screen.getByLabelText("New password"), "violet-anchor-meadow-41");
    await user.type(screen.getByLabelText("Repeat the new password"), "violet-anchor-meadow-41");
    await user.click(screen.getByRole("button", { name: "Change password" }));

    const dialog = await screen.findByRole("dialog", { name: "Confirm your identity" });
    expect(within(dialog).getByLabelText("Your password")).toHaveAttribute("type", "password");
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    expect(callsOf("ChangePassword")).toHaveLength(1);
    expect(callsOf("CompleteStepUp")).toHaveLength(0);
  });
});
