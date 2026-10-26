import { ApolloClient, ApolloLink, HttpLink, InMemoryCache } from "@apollo/client";
import { GraphQLWsLink } from "@apollo/client/link/subscriptions";
import { getMainDefinition } from "@apollo/client/utilities";
import { OperationTypeNode } from "graphql";
import { createClient } from "graphql-ws";

/** Same-origin GraphQL endpoint (D-050: POST + application/json only). */
export const GRAPHQL_PATH = "/graphql";

/** ws: or wss: URL for the same origin, derived from the page location. */
export function graphqlWsUrl(loc: Pick<Location, "protocol" | "host"> = window.location): string {
  const scheme = loc.protocol === "https:" ? "wss:" : "ws:";
  return `${scheme}//${loc.host}${GRAPHQL_PATH}`;
}

export function createApolloClient(): ApolloClient {
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
      // Connect only when the first subscription starts.
      lazy: true,
      retryAttempts: Infinity,
      shouldRetry: () => true,
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
