import { ApolloClient, ApolloLink, HttpLink, InMemoryCache } from "@apollo/client";
import { GraphQLWsLink } from "@apollo/client/link/subscriptions";
import { getMainDefinition } from "@apollo/client/utilities";
import { OperationTypeNode } from "graphql";
import { createClient } from "graphql-ws";

/** Same-origin GraphQL endpoint (D-050: POST + application/json only). */
export const GRAPHQL_PATH = "/graphql";

/**
 * WebSocket close codes of the server (internal/graph/wsconn, task 1.10).
 * 4401/4403: the session ended or its access changed, sign in again.
 * 4001: the session was rotated; reconnecting uses the new cookie.
 */
export const WS_CLOSE = {
  sessionEnded: 4401,
  accessChanged: 4403,
  sessionRotated: 4001,
  badInit: 4400,
} as const;

/** ws: or wss: URL for the same origin, derived from the page location. */
export function graphqlWsUrl(loc: Pick<Location, "protocol" | "host"> = window.location): string {
  const scheme = loc.protocol === "https:" ? "wss:" : "ws:";
  return `${scheme}//${loc.host}${GRAPHQL_PATH}`;
}

function closeCode(event: unknown): number | undefined {
  if (typeof event === "object" && event !== null && "code" in event) {
    const { code } = event;
    if (typeof code === "number") return code;
  }
  return undefined;
}

/** True when a WebSocket close means the user has to sign in again. */
export function wsRequiresLogin(event: unknown): boolean {
  const code = closeCode(event);
  return code === WS_CLOSE.sessionEnded || code === WS_CLOSE.accessChanged;
}

/** Retry everything except closes that need a new login or a code fix. */
export function wsShouldRetry(event: unknown): boolean {
  return !wsRequiresLogin(event) && closeCode(event) !== WS_CLOSE.badInit;
}

/** Default reaction to a lost session: full navigation to the login page. */
export function redirectToLogin(
  loc: Pick<Location, "pathname" | "assign"> = window.location,
): void {
  if (loc.pathname !== "/login") loc.assign("/login");
}

export interface ApolloClientOptions {
  /** Called when the server closes the subscription socket with 4401/4403. */
  onAuthLost?: () => void;
}

export function createApolloClient({
  onAuthLost = redirectToLogin,
}: ApolloClientOptions = {}): ApolloClient {
  const httpLink = new HttpLink({
    uri: GRAPHQL_PATH,
    // Session cookie is __Host-, SameSite=Strict; never send it cross-origin.
    credentials: "same-origin",
    // Never fall back to GET: the server rejects it (CSRF surface).
    useGETForQueries: false,
  });

  const wsLink = new GraphQLWsLink(
    createClient({
      url: graphqlWsUrl(),
      // The server authenticates the upgrade with the session cookie only
      // and rejects connection_init payloads: no connectionParams.
      // Connect only when the first subscription starts.
      lazy: true,
      retryAttempts: Infinity,
      shouldRetry: wsShouldRetry,
      on: {
        closed: (event) => {
          if (wsRequiresLogin(event)) onAuthLost();
        },
      },
    }),
  );

  const link = ApolloLink.split(
    ({ query }) => {
      const def = getMainDefinition(query);
      return def.kind === "OperationDefinition" && def.operation === OperationTypeNode.SUBSCRIPTION;
    },
    wsLink,
    httpLink,
  );

  return new ApolloClient({ link, cache: new InMemoryCache() });
}
