import { ApolloLink } from "@apollo/client";
import { getMainDefinition } from "@apollo/client/utilities";
import { render } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { FormattedExecutionResult } from "graphql";
import { createMemoryRouter } from "react-router";
import { Observable } from "rxjs";

import { AppProviders } from "@/app/app";
import { routes } from "@/app/router";
import type { MeQuery, Role } from "@/graphql/gen/graphql";
import { createApolloClient } from "@/lib/apollo";

export type Vars = Record<string, unknown>;
export type Handler = (
  variables: Vars,
) => FormattedExecutionResult | Promise<FormattedExecutionResult>;

export interface Call {
  name: string;
  variables: Vars;
  /** True for subscriptions (each one is a stream, D-197). */
  subscription?: boolean;
}

/**
 * A terminating link that answers operations by name: a tiny fake server.
 * Unknown queries/mutations fail the test loudly; unknown subscriptions stay
 * open without events (like an idle stream).
 */
export function mockLink(handlers: Record<string, Handler>, calls: Call[]): ApolloLink {
  return new ApolloLink(
    (operation) =>
      new Observable<FormattedExecutionResult>((observer) => {
        const name = operation.operationName ?? "";
        const variables = operation.variables;
        const def = getMainDefinition(operation.query);
        const subscription = def.kind === "OperationDefinition" && def.operation === "subscription";
        calls.push({ name, variables, ...(subscription ? { subscription } : {}) });
        const handler = handlers[name];
        if (!handler) {
          if (subscription) return;
          observer.error(new Error(`unmocked operation ${name}`));
          return;
        }
        let closed = false;
        Promise.resolve(handler(variables)).then(
          (result) => {
            if (closed) return;
            observer.next(result);
            observer.complete();
          },
          (err: unknown) => {
            if (!closed) observer.error(err);
          },
        );
        return () => {
          closed = true;
        };
      }),
  );
}

/** A GraphQL error response with an apierror code. */
export function gqlError(
  code: string,
  message = code.toLowerCase(),
  field?: string,
): FormattedExecutionResult {
  return {
    data: null,
    errors: [{ message, extensions: { code, ...(field ? { field } : {}) } }],
  };
}

export function ok(data: Record<string, unknown>): FormattedExecutionResult {
  return { data };
}

type Me = NonNullable<MeQuery["me"]>;

export function meFixture(
  overrides: Partial<Me> = {},
  role: Role = "ADMIN",
): { me: Me & { __typename: "User" } } {
  return {
    me: {
      __typename: "User",
      id: "6f1c3e0a-0000-4000-8000-000000000001",
      email: "ada@example.com",
      displayName: "Ada Lovelace",
      role,
      passwordChangeRequired: false,
      twoFactorRequired: false,
      twoFactorEnrollmentDeadline: null,
      ...overrides,
    },
  };
}

export function serverInfoFixture(insecureTransport = false) {
  return {
    serverInfo: { __typename: "ServerInfo", version: "1.0.0", insecureTransport, devMode: false },
  };
}

/** Handlers every page needs: a secure server and a signed-in admin. */
export function baseHandlers(
  me: ReturnType<typeof meFixture> = meFixture(),
): Record<string, Handler> {
  return {
    ServerInfo: () => ok(serverInfoFixture(false)),
    Me: () => ok(me),
    // The top-bar bell (task 6.10) on every shell page: an empty inbox.
    NotificationBell: () =>
      ok({
        unreadNotificationCount: 0,
        notifications: {
          __typename: "NotificationConnection",
          edges: [],
          pageInfo: { __typename: "PageInfo", hasNextPage: false, endCursor: null },
        },
      }),
  };
}

/** Renders the whole app (real routes, real links above the fake server) at path. */
export function renderApp(path: string, handlers: Record<string, Handler>) {
  const calls: Call[] = [];
  const client = createApolloClient({ terminatingLink: mockLink(handlers, calls) });
  const router = createMemoryRouter(routes, { initialEntries: [path] });
  const user = userEvent.setup();
  const view = render(<AppProviders client={client} router={router} />);
  const callsOf = (name: string) => calls.filter((c) => c.name === name);
  /** Names of the subscriptions (streams) opened so far. */
  const subscriptions = () => calls.filter((c) => c.subscription).map((c) => c.name);
  return { ...view, router, client, calls, callsOf, subscriptions, user };
}
