import { ApolloClient, ApolloLink, HttpLink, InMemoryCache } from "@apollo/client";
import { ErrorLink } from "@apollo/client/link/error";
import { getMainDefinition } from "@apollo/client/utilities";
import { type FormattedExecutionResult, OperationTypeNode, print } from "graphql";
import { createClient, NetworkError, type RequestParams, type Sink } from "graphql-sse";
import { Observable } from "rxjs";

import { ApiCode, apiErrors } from "./api-error";
import { type AuthEvent, emitAuthEvent } from "./auth-events";
import { createSharedStreams, type SharedStreams } from "./stream-sharing";

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

/**
 * Backoff after the n-th consecutive 429 (the session is at its stream
 * cap, D-197): 5s doubling up to 2 minutes, jittered between half and the
 * full value, so tabs over the budget do not hammer the server in step.
 */
export function limitRetryDelay(attempt: number, random: () => number = Math.random): number {
  const base = Math.min(120_000, 5000 * 2 ** Math.min(attempt, 5));
  return Math.round(base / 2 + (random() * base) / 2);
}

/**
 * Streams currently refused with 429 (too many open streams for the
 * session). The shell shows a "too many open tabs" hint while it is not
 * zero (useSyncExternalStore).
 */
export const streamLimit = (() => {
  let limited = 0;
  const listeners = new Set<() => void>();
  const emit = () => {
    for (const l of [...listeners]) l();
  };
  return {
    /** A stream entered (true) or left (false) the refused state. */
    report: (entered: boolean) => {
      limited = Math.max(0, limited + (entered ? 1 : -1));
      emit();
    },
    count: () => limited,
    subscribe: (listener: () => void) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
  };
})();

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
  /** Backoff before a retry after 429 (tests pass 0). */
  limitDelay?: (attempt: number) => number;
  /** Called when the stream enters (true) or leaves (false) the 429 state. */
  onLimited?: (limited: boolean) => void;
}

/**
 * Apollo link for subscriptions over Server-Sent Events (task 2.13). Each
 * subscription is one POST /graphql stream. The link reopens the stream
 * after SESSION_ROTATED at once and after UNAVAILABLE, network errors and
 * 5xx with a backoff, and after 429 (stream cap reached) with a longer
 * one (limitDelay, reported through onLimited); it hands
 * UNAUTHENTICATED/FORBIDDEN (and a 401 refusal) to onAuthLost and ends
 * the subscription with that result.
 */
export function createSSELink({
  client,
  onAuthLost,
  delay = retryDelay,
  limitDelay = limitRetryDelay,
  onLimited,
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
        let limitAttempt = 0;
        let limited = false;
        const setLimited = (v: boolean) => {
          if (limited === v) return;
          limited = v;
          onLimited?.(v);
        };

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
              limitAttempt = 0;
              setLimited(false);
              observer.next(result);
            },
            error(err: unknown) {
              if (closed) return;
              const status = refusalStatus(err);
              if (status === 429) {
                setLimited(true);
                retry(limitDelay(limitAttempt++));
                return;
              }
              setLimited(false);
              if (status === 401) {
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
          setLimited(false);
          if (timer !== undefined) clearTimeout(timer);
          dispose?.();
        };
      }),
  );
}

/**
 * Context key of a subscription shared across tabs (D-197): the value is
 * the share key and must name the account, e.g. `account:<user id>`.
 * Only subscriptions whose events are the same for every tab of the
 * browser may be shared (no per-tab variables such as a log offset).
 */
export const SHARED_STREAM = "sharedStream";

/** The context of a shared subscription: `useSubscription(Q, { context: sharedStream(key) })`. */
export function sharedStream(key: string): Record<string, string> {
  return { [SHARED_STREAM]: key };
}

export interface SharedStreamLinkOptions {
  /** The tab's hub; null: no sharing (every tab opens its own streams). */
  hub: SharedStreams | null;
  /** Called in a follower tab when the leader's stream ended the session. */
  onAuthLost: () => void;
}

/**
 * Link that shares subscriptions marked with sharedStream() across the
 * tabs of the browser: the leading tab forwards to the SSE link and
 * relays every result; the other tabs only receive. A relayed
 * UNAUTHENTICATED/FORBIDDEN end (or a 401 refusal) runs onAuthLost in the
 * receiving tab too (the leader's SSE link already ran it there).
 */
export function createSharedStreamLink({ hub, onAuthLost }: SharedStreamLinkOptions): ApolloLink {
  return new ApolloLink((operation, forward) => {
    const key: unknown = operation.getContext()[SHARED_STREAM];
    if (!hub || typeof key !== "string" || key === "") return forward(operation);
    const shareKey = `${operation.operationName ?? ""}|${key}|${JSON.stringify(operation.variables)}`;
    return new Observable<FormattedExecutionResult>((observer) => {
      let done = false;
      const unsubscribe = hub.subscribe<FormattedExecutionResult>(
        shareKey,
        (sink) => {
          const sub = forward(operation).subscribe({
            next: (r) => {
              sink.next(r as FormattedExecutionResult);
            },
            error: (err: unknown) => {
              sink.end({
                kind: "error",
                message: err instanceof Error ? err.message : "stream failed",
                authLost: refusalStatus(err) === 401,
              });
            },
            complete: () => {
              sink.end({ kind: "complete" });
            },
          });
          return () => {
            sub.unsubscribe();
          };
        },
        {
          next(result, local) {
            if (done) return;
            if (!local && streamRequiresLogin(streamEndCode(result))) onAuthLost();
            observer.next(result);
          },
          end(end, local) {
            if (done) return;
            done = true;
            if (end.kind === "complete") {
              observer.complete();
              return;
            }
            if (!local && end.authLost) onAuthLost();
            observer.error(new Error(end.message));
          },
        },
      );
      return () => {
        done = true;
        unsubscribe();
      };
    });
  });
}

/**
 * Operations whose auth errors the caller handles itself: a failed login
 * answers UNAUTHENTICATED by design, the session gate (RequireSession)
 * reacts to `me`, and logout ends on the login page anyway.
 */
export const AUTH_SELF_HANDLED: ReadonlySet<string> = new Set(["Login", "Me", "Logout"]);

/**
 * The auth event an operation's error implies, if any: UNAUTHENTICATED
 * (or an HTTP 401) → sign in again; SECOND_FACTOR_REQUIRED → finish the
 * login; PASSWORD_CHANGE_REQUIRED / REQUIRES_2FA_ENROLLMENT → the session
 * is restricted, refresh `me` so the gate shows the forced flow.
 * STEP_UP_REQUIRED is not global: useStepUp() handles it per operation.
 */
export function authEventFor(
  operationName: string | undefined,
  error: unknown,
): AuthEvent | undefined {
  if (operationName && AUTH_SELF_HANDLED.has(operationName)) return undefined;
  const codes = new Set(apiErrors(error).map((e) => e.code));
  if (codes.has(ApiCode.unauthenticated)) return "unauthenticated";
  if (codes.has(ApiCode.secondFactorRequired)) return "second-factor-required";
  if (codes.has(ApiCode.passwordChangeRequired) || codes.has(ApiCode.requires2FAEnrollment)) {
    return "restricted";
  }
  return undefined;
}

/** Error link: maps auth-related errors of any operation to auth events. */
export function createAuthErrorLink(emit: (event: AuthEvent) => void = emitAuthEvent): ApolloLink {
  return new ErrorLink(({ error, operation }) => {
    const event = authEventFor(operation.operationName, error);
    if (event) emit(event);
  });
}

/**
 * Cache policies. Users are normalized by id, so `me` and other queries
 * share one record; `me` is replaced, never merged, so a new sign-in never
 * mixes with the previous account's fields (the cache is also cleared on
 * every sign-in and sign-out).
 */
export function createCache(): InMemoryCache {
  return new InMemoryCache({
    typePolicies: {
      Query: {
        fields: {
          me: { merge: false },
          mySessions: { merge: false },
          myPasskeys: { merge: false },
        },
      },
      // Singletons without an id.
      ServerInfo: { keyFields: [] },
      TwoFactorStatus: { keyFields: [] },
      WebAuthnStatus: { keyFields: [] },
    },
  });
}

export interface ApolloClientOptions {
  /** Called when the server ends a subscription stream because the session ended. */
  onAuthLost?: () => void;
  /** Receives the auth events of failed operations (default: the auth-events bus). */
  onAuthEvent?: (event: AuthEvent) => void;
  /** Replaces the HTTP and SSE links (tests). */
  terminatingLink?: ApolloLink;
  /**
   * The cross-tab stream hub (D-197). Default: a BroadcastChannel hub
   * (null when unavailable); with a terminatingLink the default is null.
   */
  sharedStreams?: SharedStreams | null;
}

export function createApolloClient({
  onAuthLost = () => {
    emitAuthEvent("session-check");
  },
  onAuthEvent = emitAuthEvent,
  terminatingLink,
  sharedStreams = terminatingLink ? null : createSharedStreams(),
}: ApolloClientOptions = {}): ApolloClient {
  const transport = terminatingLink ?? createTransportLink(onAuthLost);
  return new ApolloClient({
    link: ApolloLink.from([
      createAuthErrorLink(onAuthEvent),
      createSharedStreamLink({ hub: sharedStreams, onAuthLost }),
      transport,
    ]),
    cache: createCache(),
  });
}

function createTransportLink(onAuthLost: () => void): ApolloLink {
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
    onLimited: (limited) => {
      streamLimit.report(limited);
    },
  });

  return ApolloLink.split(
    ({ query }) => {
      const def = getMainDefinition(query);
      return def.kind === "OperationDefinition" && def.operation === OperationTypeNode.SUBSCRIPTION;
    },
    sseLink,
    httpLink,
  );
}
