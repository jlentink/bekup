import { ApolloClient, ApolloLink, HttpLink, InMemoryCache } from "@apollo/client";
import { getMainDefinition } from "@apollo/client/utilities";
import { type FormattedExecutionResult, OperationTypeNode, print } from "graphql";
import { createClient, NetworkError, type RequestParams, type Sink } from "graphql-sse";
import { Observable } from "rxjs";

/** Same-origin GraphQL endpoint (D-050: POST + application/json only). */
export const GRAPHQL_PATH = "/graphql";

/**
 * Codes of the final error event with which the server ends an event
 * stream (internal/graph/streams, task 2.13). UNAUTHENTICATED/FORBIDDEN:
 * the session ended or its access changed, sign in again. SESSION_ROTATED:
 * the session was renewed; reopening the stream uses the new cookie.
 * UNAVAILABLE: shutdown or a failed session check, retry later.
 */
export const STREAM_END = {
  sessionEnded: "UNAUTHENTICATED",
  accessChanged: "FORBIDDEN",
  sessionRotated: "SESSION_ROTATED",
  unavailable: "UNAVAILABLE",
} as const;

type StreamEndCode = (typeof STREAM_END)[keyof typeof STREAM_END];

const END_CODES: ReadonlySet<string> = new Set(Object.values(STREAM_END));

/**
 * The end code of a stream result: set when the result carries only
 * errors (no data) and the first one has one of the STREAM_END codes.
 */
export function streamEndCode(result: FormattedExecutionResult): StreamEndCode | undefined {
  if (result.data != null) return undefined;
  const code = result.errors?.[0]?.extensions?.["code"];
  return typeof code === "string" && END_CODES.has(code) ? (code as StreamEndCode) : undefined;
}

/** True when a stream end means the user has to sign in again. */
export function streamRequiresLogin(code: string | undefined): boolean {
  return code === STREAM_END.sessionEnded || code === STREAM_END.accessChanged;
}

/** HTTP status of a refused stream request (undefined: no response). */
function refusalStatus(err: unknown): number | undefined {
  if (!(err instanceof NetworkError)) return undefined;
  const res: unknown = err.response;
  if (
    typeof res === "object" &&
    res !== null &&
    "status" in res &&
    typeof res.status === "number"
  ) {
    return res.status;
  }
  return undefined;
}

/**
 * Whether opening a stream should be retried after err: network failures,
 * 429 and 5xx yes; 401 (signed out), 403 (origin or restricted session)
 * and other client errors no.
 */
export function streamShouldRetry(err: unknown): boolean {
  if (!(err instanceof NetworkError)) return false;
  const status = refusalStatus(err);
  return status === undefined || status === 429 || status >= 500;
}

/** Backoff before the n-th consecutive retry (1s doubling up to 30s, jittered). */
export function retryDelay(attempt: number, random: () => number = Math.random): number {
  const base = Math.min(30_000, 1000 * 2 ** Math.min(attempt, 5));
  return Math.round(base / 2 + (random() * base) / 2);
}

/** Default reaction to a lost session: full navigation to the login page. */
export function redirectToLogin(
  loc: Pick<Location, "pathname" | "assign"> = window.location,
): void {
  if (loc.pathname !== "/login") loc.assign("/login");
}

/**
 * The part of the graphql-sse client the link uses. Results arrive as
 * JSON, i.e. formatted execution results.
 */
export interface StreamClient {
  subscribe(request: RequestParams, sink: Sink<FormattedExecutionResult>): () => void;
}

export interface SSELinkOptions {
  /** The graphql-sse client (distinct connections mode, no own retries). */
  client: StreamClient;
  /** Called when a stream ends with UNAUTHENTICATED/FORBIDDEN or is refused with 401. */
  onAuthLost: () => void;
  /** Backoff before a retry (tests pass 0). */
  delay?: (attempt: number) => number;
}

/**
 * Apollo link for subscriptions over Server-Sent Events (task 2.13). Each
 * subscription is one POST /graphql stream. The link reopens the stream
 * after SESSION_ROTATED at once and after UNAVAILABLE, network errors, 429
 * and 5xx with a backoff; it hands UNAUTHENTICATED/FORBIDDEN (and a 401
 * refusal) to onAuthLost and ends the subscription with that result.
 */
export function createSSELink({
  client,
  onAuthLost,
  delay = retryDelay,
}: SSELinkOptions): ApolloLink {
  return new ApolloLink(
    (operation) =>
      new Observable<FormattedExecutionResult>((observer) => {
        const request = {
          query: print(operation.query),
          variables: operation.variables,
          ...(operation.operationName ? { operationName: operation.operationName } : {}),
        };
        let dispose: (() => void) | undefined;
        let timer: ReturnType<typeof setTimeout> | undefined;
        let closed = false;
        let attempt = 0;

        const retry = (wait: number) => {
          dispose = undefined;
          if (closed) return;
          timer = setTimeout(start, wait);
        };

        function start() {
          timer = undefined;
          if (closed) return;
          let end: { code: StreamEndCode; result: FormattedExecutionResult } | undefined;
          const sink: Sink<FormattedExecutionResult> = {
            next(result) {
              const code = streamEndCode(result);
              if (code) {
                end = { code, result };
                return;
              }
              attempt = 0;
              observer.next(result);
            },
            error(err: unknown) {
              if (closed) return;
              if (refusalStatus(err) === 401) {
                closed = true;
                onAuthLost();
                observer.error(err);
                return;
              }
              if (streamShouldRetry(err)) {
                retry(delay(attempt++));
                return;
              }
              closed = true;
              observer.error(err);
            },
            complete() {
              if (closed) return;
              if (!end) {
                closed = true;
                observer.complete();
                return;
              }
              switch (end.code) {
                case STREAM_END.sessionRotated:
                  retry(0);
                  return;
                case STREAM_END.unavailable:
                  retry(delay(attempt++));
                  return;
                default:
                  closed = true;
                  onAuthLost();
                  observer.next(end.result);
                  observer.complete();
              }
            },
          };
          dispose = client.subscribe(request, sink);
        }

        start();
        return () => {
          closed = true;
          if (timer !== undefined) clearTimeout(timer);
          dispose?.();
        };
      }),
  );
}

export interface ApolloClientOptions {
  /** Called when the server ends a subscription stream because the session ended. */
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

  const sse = createClient({
    url: GRAPHQL_PATH,
    // The stream is authenticated by the session cookie only.
    credentials: "same-origin",
    // One POST stream per subscription; the link does the retries.
    singleConnection: false,
    retryAttempts: 0,
  });
  const sseLink = createSSELink({
    client: {
      // graphql-sse types results as ExecutionResult; on the wire they are
      // the formatted (JSON) results the link handles.
      subscribe: (request, sink) => sse.subscribe(request, sink as unknown as Sink),
    },
    onAuthLost,
  });

  const link = ApolloLink.split(
    ({ query }) => {
      const def = getMainDefinition(query);
      return def.kind === "OperationDefinition" && def.operation === OperationTypeNode.SUBSCRIPTION;
    },
    sseLink,
    httpLink,
  );

  return new ApolloClient({ link, cache: new InMemoryCache() });
}
