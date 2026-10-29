import { ApolloProvider } from "@apollo/client/react";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";

import { createApolloClient } from "@/lib/apollo";
import {
  baseHandlers,
  type Call,
  gqlError,
  type Handler,
  meFixture,
  mockLink,
  ok,
  renderApp,
} from "@/test/mock-api";

import { UserPicker } from "./user-picker";
import { pickerOptions, type PickedUser } from "./user-picker-model";

const ME_ID = "6f1c3e0a-0000-4000-8000-000000000001";
const BOB_ID = "6f1c3e0a-0000-4000-8000-000000000002";
const TEMP = "Plaid-Otter-Lantern-93";

function userNode(overrides: Record<string, unknown> = {}) {
  return {
    __typename: "User",
    id: BOB_ID,
    email: "bob@example.com",
    displayName: "Bob Builder",
    role: "USER",
    disabled: false,
    lockedUntil: null,
    passwordChangeRequired: false,
    temporaryPasswordExpiresAt: null,
    twoFactorRequired: false,
    twoFactorEnrollmentDeadline: null,
    twoFactorEnrolled: false,
    lastSeenAt: null,
    emailVerified: true,
    createdAt: "2026-10-01T10:00:00Z",
    updatedAt: "2026-10-01T10:00:00Z",
    deletedAt: null,
    ...overrides,
  };
}

const meNode = () =>
  userNode({
    id: ME_ID,
    email: "ada@example.com",
    displayName: "Ada Lovelace",
    role: "ADMIN",
    twoFactorRequired: true,
    twoFactorEnrolled: true,
    lastSeenAt: "2026-10-28T09:00:00Z",
  });

function usersPage(nodes: Record<string, unknown>[]) {
  return ok({
    users: {
      __typename: "UserConnection",
      edges: nodes.map((n) => ({ __typename: "UserEdge", cursor: String(n["id"]), node: n })),
      pageInfo: { __typename: "PageInfo", hasNextPage: false, endCursor: null },
    },
  });
}

const noJobs: Handler = () =>
  ok({
    jobs: {
      __typename: "JobConnection",
      edges: [],
      pageInfo: { __typename: "PageInfo", hasNextPage: false, endCursor: null },
    },
  });

/** Step-up handlers: TOTP "654321" confirms; returns a flag read by mutations. */
function stepUp() {
  const state = { stepped: false };
  const handlers: Record<string, Handler> = {
    BeginStepUp: () =>
      ok({
        beginStepUp: { __typename: "StepUpChallenge", methods: ["TOTP"], passkeyOptions: null },
      }),
    CompleteStepUp: (vars) => {
      state.stepped = vars["code"] === "654321";
      return ok({
        completeStepUp: {
          __typename: "StepUpResult",
          stepUpAt: "2026-10-28T10:00:00Z",
          aal2At: "2026-10-28T10:00:00Z",
          aal: 2,
        },
      });
    },
  };
  /** Wraps a result so it needs a step-up first. */
  const guarded =
    (h: Handler): Handler =>
    (vars) =>
      state.stepped ? h(vars) : gqlError("STEP_UP_REQUIRED", "please confirm your identity again");
  return { state, handlers, guarded };
}

async function confirmStepUp(user: ReturnType<typeof userEvent.setup>) {
  const dialog = await screen.findByRole("dialog", { name: "Confirm your identity" });
  await user.type(within(dialog).getByLabelText("6-digit code"), "654321");
  await user.click(within(dialog).getByRole("button", { name: "Confirm" }));
}

function detailHandlers(node: Record<string, unknown>): Record<string, Handler> {
  return {
    AdminUser: () => ok({ user: node }),
    AdminUserSessions: () => ok({ userSessions: [] }),
    AdminOwnedJobs: noJobs,
  };
}

describe("route guard", () => {
  it.each([
    ["MAINTAINER", "/admin/users"],
    ["USER", "/admin/users/" + BOB_ID],
    ["MAINTAINER", "/admin/two-factor"],
  ] as const)("hides the admin pages from a %s (%s)", async (role, path) => {
    const { callsOf } = renderApp(path, baseHandlers(meFixture({}, role)));
    expect(await screen.findByRole("heading", { name: "Admins only" })).toBeInTheDocument();
    expect(callsOf("AdminUsers")).toHaveLength(0);
    expect(callsOf("AdminUser")).toHaveLength(0);
    expect(callsOf("AdminTwoFactorPolicy")).toHaveLength(0);
  });
});

describe("users list", () => {
  it("lists accounts with their state and filters by search", async () => {
    const { user, callsOf } = renderApp("/admin/users", {
      ...baseHandlers(),
      AdminUsers: (vars) => {
        const f = (vars["filter"] ?? {}) as Record<string, unknown>;
        return usersPage(
          f["search"] === "bob"
            ? [userNode()]
            : [
                meNode(),
                userNode({
                  lockedUntil: "2999-01-01T00:00:00Z",
                  passwordChangeRequired: true,
                  temporaryPasswordExpiresAt: "2999-01-01T00:00:00Z",
                }),
              ],
        );
      },
    });
    const table = await screen.findByRole("table");
    const bob = within(table).getByRole("link", { name: "Bob Builder" }).closest("tr");
    if (!bob) throw new Error("no row");
    expect(within(bob).getByText("Locked")).toBeInTheDocument();
    expect(within(bob).getByText("Temporary password")).toBeInTheDocument();
    const ada = within(table).getByRole("link", { name: "Ada Lovelace" }).closest("tr");
    if (!ada) throw new Error("no row");
    expect(within(ada).getByText("Enrolled")).toBeInTheDocument();
    expect(within(ada).getByText("You")).toBeInTheDocument();
    // D-184: last activity and enrollment come from the API, not the policy.
    expect(within(ada).getByRole("time")).toHaveAttribute("datetime", "2026-10-28T09:00:00Z");
    expect(within(bob).getByText("Never")).toBeInTheDocument();
    expect(within(bob).getByText("None")).toBeInTheDocument();

    await user.type(screen.getByLabelText("Search users"), "bob");
    await waitFor(() => {
      expect(screen.queryByRole("link", { name: "Ada Lovelace" })).toBeNull();
    });
    expect(callsOf("AdminUsers").at(-1)?.variables["filter"]).toEqual({ search: "bob" });
  });
});

describe("create user", () => {
  it("shows the temporary password once, after a step-up, and never again", async () => {
    const su = stepUp();
    const { user, client, callsOf, router } = renderApp("/admin/users", {
      ...baseHandlers(),
      ...su.handlers,
      ...detailHandlers(userNode({ passwordChangeRequired: true })),
      AdminUsers: () => usersPage([meNode()]),
      AdminCreateUser: su.guarded((vars) => {
        expect(vars["input"]).toEqual({
          email: "bob@example.com",
          displayName: "Bob Builder",
          role: "USER",
        });
        return ok({
          createUser: {
            __typename: "CreatedUser",
            user: {
              __typename: "User",
              id: BOB_ID,
              email: "bob@example.com",
              displayName: "Bob Builder",
              role: "USER",
            },
            temporaryPassword: {
              __typename: "TemporaryPassword",
              password: TEMP,
              expiresAt: "2026-10-31T10:00:00Z",
            },
          },
        });
      }),
    });
    await user.click(await screen.findByRole("button", { name: "New user" }));
    const form = await screen.findByRole("dialog", { name: "Create an account" });
    await user.click(within(form).getByRole("button", { name: "Create account" }));
    expect(await within(form).findByText("Enter a valid e-mail address.")).toBeInTheDocument();
    expect(callsOf("AdminCreateUser")).toHaveLength(0);

    await user.type(within(form).getByLabelText("E-mail"), "bob@example.com");
    await user.type(within(form).getByLabelText("Name"), "Bob Builder");
    await user.click(within(form).getByRole("button", { name: "Create account" }));
    await confirmStepUp(user);

    const reveal = await screen.findByRole("dialog", { name: /bob@example.com can now sign in/ });
    expect(within(reveal).getByText(TEMP)).toBeInTheDocument();
    expect(within(reveal).getByText(/will not be shown again/)).toBeInTheDocument();
    expect(within(reveal).getByText(/Expires/)).toBeInTheDocument();
    expect(callsOf("AdminCreateUser")).toHaveLength(2);

    // Escape does not close it before the acknowledgement.
    await user.keyboard("{Escape}");
    expect(screen.getByText(TEMP)).toBeInTheDocument();
    const done = within(reveal).getByRole("button", { name: "Done" });
    expect(done).toBeDisabled();
    await user.click(within(reveal).getByLabelText(/I have handed over/));
    await user.click(done);
    await waitFor(() => {
      expect(screen.queryByText(TEMP)).toBeNull();
    });

    // Never in the cache, so no refetch or navigation can render it again.
    expect(JSON.stringify(client.extract())).not.toContain(TEMP);
    await router.navigate(`/admin/users/${BOB_ID}`);
    expect(await screen.findByRole("heading", { name: "Bob Builder" })).toBeInTheDocument();
    await router.navigate("/admin/users");
    expect(await screen.findByRole("table")).toBeInTheDocument();
    expect(screen.queryByText(TEMP)).toBeNull();
  });

  it("puts a taken e-mail address on the field", async () => {
    const { user } = renderApp("/admin/users", {
      ...baseHandlers(),
      AdminUsers: () => usersPage([]),
      AdminCreateUser: () => gqlError("CONFLICT", "that e-mail address is already in use"),
    });
    await user.click(await screen.findByRole("button", { name: "New user" }));
    const form = await screen.findByRole("dialog", { name: "Create an account" });
    await user.type(within(form).getByLabelText("E-mail"), "taken@example.com");
    await user.type(within(form).getByLabelText("Name"), "Taken");
    await user.click(within(form).getByRole("button", { name: "Create account" }));
    expect(
      await within(form).findByText("That e-mail address is already in use."),
    ).toBeInTheDocument();
  });
});

describe("user detail", () => {
  it("blocks resetting your own second factors and deleting yourself", async () => {
    renderApp(`/admin/users/${ME_ID}`, { ...baseHandlers(), ...detailHandlers(meNode()) });
    const reset = await screen.findByRole("button", { name: "Reset second factors" });
    expect(reset).toBeDisabled();
    expect(screen.getByText(/You cannot reset your own second factors/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete account" })).toBeNull();
    expect(screen.getByText("You cannot delete your own account.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Disable account" })).toBeNull();
  });

  it("resets another account's second factors after confirm and step-up", async () => {
    const su = stepUp();
    const { user, callsOf } = renderApp(`/admin/users/${BOB_ID}`, {
      ...baseHandlers(),
      ...su.handlers,
      ...detailHandlers(userNode()),
      AdminResetUserSecondFactors: su.guarded((vars) => {
        expect(vars["userId"]).toBe(BOB_ID);
        return ok({
          resetUserSecondFactors: {
            __typename: "SecondFactorReset",
            user: userNode(),
            removedTotp: true,
            removedPasskeys: 2,
            removedRecoveryCodes: 10,
            revokedSessions: 1,
          },
        });
      }),
    });
    await user.click(await screen.findByRole("button", { name: "Reset second factors" }));
    const confirm = await screen.findByRole("dialog", { name: "Reset all second factors?" });
    expect(within(confirm).getByText(/must enroll a new second factor/)).toBeInTheDocument();
    await user.click(within(confirm).getByRole("button", { name: "Reset second factors" }));
    await confirmStepUp(user);
    expect(
      await screen.findByText(/TOTP removed, 2 passkeys and 10 recovery codes removed/),
    ).toBeInTheDocument();
    expect(callsOf("AdminResetUserSecondFactors")).toHaveLength(2);
  });

  it("explains the last-admin conflict on delete", async () => {
    const { user } = renderApp(`/admin/users/${BOB_ID}`, {
      ...baseHandlers(),
      ...detailHandlers(userNode({ role: "ADMIN" })),
      AdminDeleteUser: () => gqlError("CONFLICT", "the last active admin cannot be removed"),
    });
    await user.click(await screen.findByRole("button", { name: "Delete account" }));
    const dialog = await screen.findByRole("dialog", { name: "Delete this account?" });
    const submit = within(dialog).getByRole("button", { name: "Delete account" });
    expect(submit).toBeDisabled();
    await user.type(within(dialog).getByLabelText(/Type bob@example.com/), "bob@example.com");
    await user.click(submit);
    expect(await within(dialog).findByText(/This is the last active admin/)).toBeInTheDocument();
  });

  it("confirms a role change (sessions revoked) and saves it with a step-up", async () => {
    const su = stepUp();
    const { user, callsOf } = renderApp(`/admin/users/${BOB_ID}`, {
      ...baseHandlers(),
      ...su.handlers,
      ...detailHandlers(userNode()),
      AdminUpdateUser: su.guarded((vars) =>
        ok({ updateUser: userNode({ role: (vars["input"] as { role: string }).role }) }),
      ),
    });
    await user.click(await screen.findByRole("radio", { name: /^Maintainer/ }));
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    const confirm = await screen.findByRole("dialog", { name: "Change the role?" });
    expect(
      within(confirm).getByText(/All sessions of this account are revoked/),
    ).toBeInTheDocument();
    await user.click(within(confirm).getByRole("button", { name: "Change role" }));
    await confirmStepUp(user);
    expect(await screen.findByText("Account updated.")).toBeInTheDocument();
    expect(callsOf("AdminUpdateUser").at(-1)?.variables).toEqual({
      id: BOB_ID,
      input: { role: "MAINTAINER" },
    });
  });

  it("shows a reset password once", async () => {
    const { user, client } = renderApp(`/admin/users/${BOB_ID}`, {
      ...baseHandlers(),
      ...detailHandlers(userNode()),
      AdminResetUserPassword: () =>
        ok({
          resetUserPassword: {
            __typename: "TemporaryPassword",
            password: TEMP,
            expiresAt: "2026-10-31T10:00:00Z",
          },
        }),
    });
    await user.click(await screen.findByRole("button", { name: "Reset password" }));
    const confirm = await screen.findByRole("dialog", { name: "Reset the password?" });
    await user.click(within(confirm).getByRole("button", { name: "Reset password" }));
    const reveal = await screen.findByRole("dialog", { name: /New temporary password/ });
    expect(within(reveal).getByText(TEMP)).toBeInTheDocument();
    await user.click(within(reveal).getByLabelText(/I have handed over/));
    await user.click(within(reveal).getByRole("button", { name: "Done" }));
    await waitFor(() => {
      expect(screen.queryByText(TEMP)).toBeNull();
    });
    expect(JSON.stringify(client.extract())).not.toContain(TEMP);
  });

  it("lists owned jobs by owner filter and offers only other jobs (D-184)", async () => {
    const job = (id: string, name: string) => ({
      __typename: "Job",
      id,
      name,
      slug: name,
      enabled: true,
    });
    const { user, callsOf } = renderApp(`/admin/users/${BOB_ID}`, {
      ...baseHandlers(),
      ...detailHandlers(userNode({ lastSeenAt: "2026-10-27T08:00:00Z", twoFactorEnrolled: true })),
      AdminOwnedJobs: () =>
        ok({
          jobs: {
            __typename: "JobConnection",
            edges: [
              {
                __typename: "JobEdge",
                node: {
                  ...job("j-1", "orders-nightly"),
                  owners: [
                    { __typename: "JobOwner", userId: BOB_ID, addedAt: "2026-10-20T10:00:00Z" },
                  ],
                },
              },
            ],
            pageInfo: { __typename: "PageInfo", hasNextPage: false, endCursor: null },
          },
        }),
      AdminAssignableJobs: () =>
        ok({
          jobs: {
            __typename: "JobConnection",
            edges: [
              { __typename: "JobEdge", node: job("j-1", "orders-nightly") },
              { __typename: "JobEdge", node: job("j-2", "billing-hourly") },
            ],
            pageInfo: { __typename: "PageInfo", hasNextPage: false },
          },
        }),
    });
    const owned = await screen.findByRole("list", { name: "Owned jobs" });
    expect(within(owned).getByRole("link", { name: "orders-nightly" })).toBeInTheDocument();
    expect(callsOf("AdminOwnedJobs")[0]?.variables).toMatchObject({ ownerId: BOB_ID });
    expect(screen.getAllByText("Enrolled").length).toBeGreaterThan(0);

    await user.click(screen.getByRole("button", { name: "Assign jobs" }));
    const dialog = await screen.findByRole("dialog", { name: "Assign jobs" });
    expect(
      await within(dialog).findByRole("checkbox", { name: /billing-hourly/ }),
    ).toBeInTheDocument();
    expect(within(dialog).queryByRole("checkbox", { name: /orders-nightly/ })).toBeNull();
  });

  it("does not offer job ownership to admins and maintainers (D-067)", async () => {
    renderApp(`/admin/users/${BOB_ID}`, {
      ...baseHandlers(),
      ...detailHandlers(userNode({ role: "MAINTAINER" })),
    });
    expect(
      await screen.findByText(/Owner grants give rights to accounts with the User role only/),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Assign jobs" })).toBeNull();
  });
});

describe("UserPicker", () => {
  const candidates = [
    { id: "u1", email: "u1@x.io", displayName: "Una User", role: "USER", disabled: false },
    { id: "a1", email: "a1@x.io", displayName: "Ann Admin", role: "ADMIN", disabled: false },
    { id: "m1", email: "m1@x.io", displayName: "Max Maint", role: "MAINTAINER", disabled: false },
    { id: "d1", email: "d1@x.io", displayName: "Dee Disabled", role: "USER", disabled: true },
    {
      id: "x1",
      email: "x1@x.io",
      displayName: "Gone",
      role: "USER",
      disabled: true,
      deletedAt: "2026-01-01T00:00:00Z",
    },
  ] as const;

  it("blocks every role but User and drops deleted accounts", () => {
    const opts = pickerOptions(candidates, new Set(["u1"]), new Set());
    expect(opts.map((o) => [o.user.id, o.block, o.selected])).toEqual([
      ["u1", null, true],
      ["a1", "role", false],
      ["m1", "role", false],
      ["d1", "disabled", false],
    ]);
    expect(
      pickerOptions(candidates, new Set(), new Set(["u1"])).map((o) => o.user.id),
    ).not.toContain("u1");
  });

  function renderPicker(handler: Handler, onChange = vi.fn()) {
    const calls: Call[] = [];
    const client = createApolloClient({
      terminatingLink: mockLink({ UserPickerCandidates: handler }, calls),
    });
    function Host() {
      const [value, setValue] = useState<PickedUser[]>([]);
      return (
        <UserPicker
          value={value}
          onChange={(v) => {
            onChange(v);
            setValue(v);
          }}
        />
      );
    }
    render(
      <ApolloProvider client={client}>
        <Host />
      </ApolloProvider>,
    );
    return { onChange, user: userEvent.setup() };
  }

  it("selects users and explains why others are disabled", async () => {
    const { onChange, user } = renderPicker(() =>
      ok({
        userDirectory: {
          __typename: "UserDirectoryConnection",
          edges: candidates
            .filter((c) => !("deletedAt" in c))
            .map((c) => ({
              __typename: "UserDirectoryEdge",
              node: { __typename: "UserDirectoryEntry", ...c },
            })),
        },
      }),
    );
    const admin = await screen.findByRole("button", { name: /Ann Admin/ });
    expect(admin).toHaveAttribute("aria-disabled", "true");
    expect(admin).toHaveAccessibleDescription(/already have access through their role/);
    await user.click(admin);
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: /Dee Disabled/ })).toHaveAccessibleDescription(
      "Account disabled",
    );

    await user.click(screen.getByRole("button", { name: /Una User/ }));
    expect(onChange).toHaveBeenLastCalledWith([
      { id: "u1", email: "u1@x.io", displayName: "Una User", role: "USER" },
    ]);
    expect(screen.getByRole("button", { name: /^Una User/ })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await user.click(screen.getByRole("button", { name: "Remove Una User" }));
    expect(onChange).toHaveBeenLastCalledWith([]);
  });

  it("explains that only staff can browse accounts", async () => {
    renderPicker(() => gqlError("FORBIDDEN", "forbidden"));
    expect(
      await screen.findByText(/Only admins and maintainers can browse accounts/),
    ).toBeInTheDocument();
  });
});
