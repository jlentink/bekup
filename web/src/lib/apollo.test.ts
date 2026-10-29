import { ApolloLink, gql } from "@apollo/client";
import type { FormattedExecutionResult } from "graphql";
import { NetworkError, type RequestParams, type Sink } from "graphql-sse";
import { firstValueFrom, lastValueFrom, Observable, toArray } from "rxjs";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  createSharedStreamLink,
  createSSELink,
  limitRetryDelay,
  redirectToLogin,
  retryDelay,
  sharedStream,
  STREAM_END,
  streamEndCode,
  streamRequiresLogin,
  streamShouldRetry,
} from "./apollo";
import { type ChannelLike, SharedStreams } from "./stream-sharing";

const SUB = gql`
  subscription Account {
    accountEvents {
      session {
        kind
      }
    }
  }
`;

function endResult(code: string): FormattedExecutionResult {
  return { errors: [{ message: "ended", extensions: { code } }] };
}

const event: FormattedExecutionResult = {
  data: { accountEvents: { session: { kind: "REVOKED" } } },
};

/** A fake graphql-sse client: each subscribe call runs the next script. */
function fakeClient(scripts: ((sink: Sink<FormattedExecutionResult>) => void)[]) {
  const requests: unknown[] = [];
  const disposed = vi.fn();
  return {
    requests,
    disposed,
    subscribe: vi.fn((request: RequestParams, sink: Sink<FormattedExecutionResult>) => {
      requests.push(request);
      const script = scripts.shift();
      if (script)
        queueMicrotask(() => {
          script(sink);
        });
      return disposed;
    }),
  };
}

function run(link: ApolloLink) {
  return ApolloLink.execute(link, { query: SUB }, { client: {} as never });
}

function response(status: number): Response {
  return new Response(null, { status, statusText: String(status) });
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("stream end codes", () => {
  it("recognizes only data-less results with an end code", () => {
    expect(streamEndCode(endResult("UNAUTHENTICATED"))).toBe(STREAM_END.sessionEnded);
    expect(streamEndCode(endResult("SESSION_ROTATED"))).toBe(STREAM_END.sessionRotated);
    expect(streamEndCode(endResult("VALIDATION"))).toBeUndefined();
    expect(streamEndCode({ ...endResult("FORBIDDEN"), data: { x: 1 } })).toBeUndefined();
    expect(streamEndCode(event)).toBeUndefined();
  });

  it("requires a login after UNAUTHENTICATED and FORBIDDEN only", () => {
    expect(streamRequiresLogin("UNAUTHENTICATED")).toBe(true);
    expect(streamRequiresLogin("FORBIDDEN")).toBe(true);
    expect(streamRequiresLogin("SESSION_ROTATED")).toBe(false);
    expect(streamRequiresLogin("UNAVAILABLE")).toBe(false);
    expect(streamRequiresLogin(undefined)).toBe(false);
  });

  it("retries network failures, 429 and 5xx but not 4xx", () => {
    expect(streamShouldRetry(new NetworkError(new Error("offline")))).toBe(true);
    expect(streamShouldRetry(new NetworkError(response(503)))).toBe(true);
    expect(streamShouldRetry(new NetworkError(response(429)))).toBe(true);
    expect(streamShouldRetry(new NetworkError(response(401)))).toBe(false);
    expect(streamShouldRetry(new NetworkError(response(403)))).toBe(false);
    expect(streamShouldRetry(new Error("bug"))).toBe(false);
  });

  it("backs off exponentially up to 30s", () => {
    expect(retryDelay(0, () => 1)).toBe(1000);
    expect(retryDelay(1, () => 0)).toBe(1000);
    expect(retryDelay(10, () => 1)).toBe(30_000);
  });

  it("backs off longer after 429: 5s doubling up to 2 minutes, jittered", () => {
    expect(limitRetryDelay(0, () => 1)).toBe(5000);
    expect(limitRetryDelay(0, () => 0)).toBe(2500);
    expect(limitRetryDelay(2, () => 1)).toBe(20_000);
    expect(limitRetryDelay(10, () => 1)).toBe(120_000);
    expect(limitRetryDelay(10, () => 0)).toBe(60_000);
  });

  it("redirects to /login unless already there", () => {
    const assign = vi.fn();
    redirectToLogin({ pathname: "/jobs", assign });
    expect(assign).toHaveBeenCalledWith("/login");
    assign.mockClear();
    redirectToLogin({ pathname: "/login", assign });
    expect(assign).not.toHaveBeenCalled();
  });
});

describe("SSE link", () => {
  it("sends only query, variables and operationName and forwards events", async () => {
    const client = fakeClient([
      (sink) => {
        sink.next(event);
        sink.complete();
      },
    ]);
    const link = createSSELink({ client, onAuthLost: vi.fn(), delay: () => 0 });
    const got = await lastValueFrom(run(link).pipe(toArray()));
    expect(got).toEqual([event]);
    expect(Object.keys(client.requests[0] as object).sort()).toEqual([
      "operationName",
      "query",
      "variables",
    ]);
  });

  it("reopens the stream after SESSION_ROTATED without surfacing it", async () => {
    const client = fakeClient([
      (sink) => {
        sink.next(endResult("SESSION_ROTATED"));
        sink.complete();
      },
      (sink) => {
        sink.next(event);
        sink.complete();
      },
    ]);
    const onAuthLost = vi.fn();
    const link = createSSELink({ client, onAuthLost, delay: () => 0 });
    const got = await lastValueFrom(run(link).pipe(toArray()));
    expect(got).toEqual([event]);
    expect(client.subscribe).toHaveBeenCalledTimes(2);
    expect(onAuthLost).not.toHaveBeenCalled();
  });

  it("retries after UNAVAILABLE and transient refusals", async () => {
    const client = fakeClient([
      (sink) => {
        sink.next(endResult("UNAVAILABLE"));
        sink.complete();
      },
      (sink) => {
        sink.error(new NetworkError(response(503)));
      },
      (sink) => {
        sink.next(event);
        sink.complete();
      },
    ]);
    const link = createSSELink({ client, onAuthLost: vi.fn(), delay: () => 0 });
    expect(await firstValueFrom(run(link))).toEqual(event);
    expect(client.subscribe).toHaveBeenCalledTimes(3);
  });

  it.each(["UNAUTHENTICATED", "FORBIDDEN"])("hands %s to onAuthLost and ends", async (code) => {
    const client = fakeClient([
      (sink) => {
        sink.next(endResult(code));
        sink.complete();
      },
    ]);
    const onAuthLost = vi.fn();
    const link = createSSELink({ client, onAuthLost, delay: () => 0 });
    const got = await lastValueFrom(run(link).pipe(toArray()));
    expect(onAuthLost).toHaveBeenCalledTimes(1);
    expect(got).toEqual([endResult(code)]);
    expect(client.subscribe).toHaveBeenCalledTimes(1);
  });

  it("treats a 401 refusal as a lost session and a 403 as final", async () => {
    const onAuthLost = vi.fn();
    const unauth = createSSELink({
      client: fakeClient([
        (sink) => {
          sink.error(new NetworkError(response(401)));
        },
      ]),
      onAuthLost,
      delay: () => 0,
    });
    await expect(lastValueFrom(run(unauth))).rejects.toBeInstanceOf(NetworkError);
    expect(onAuthLost).toHaveBeenCalledTimes(1);

    const forbidden = createSSELink({
      client: fakeClient([
        (sink) => {
          sink.error(new NetworkError(response(403)));
        },
      ]),
      onAuthLost,
      delay: () => 0,
    });
    await expect(lastValueFrom(run(forbidden))).rejects.toBeInstanceOf(NetworkError);
    expect(onAuthLost).toHaveBeenCalledTimes(1);
  });

  it("disposes the stream on unsubscribe", () => {
    const client = fakeClient([]);
    const link = createSSELink({ client, onAuthLost: vi.fn(), delay: () => 0 });
    const sub = run(link).subscribe(() => undefined);
    sub.unsubscribe();
    expect(client.disposed).toHaveBeenCalledTimes(1);
  });
});

describe("SSE link and the stream cap", () => {
  it("retries 429 with the limit backoff and reports the limited state", async () => {
    const client = fakeClient([
      (sink) => {
        sink.error(new NetworkError(response(429)));
      },
      (sink) => {
        sink.error(new NetworkError(response(429)));
      },
      (sink) => {
        sink.next(event);
        sink.complete();
      },
    ]);
    const limitDelay = vi.fn(() => 0);
    const delay = vi.fn(() => 0);
    const onLimited = vi.fn();
    const link = createSSELink({ client, onAuthLost: vi.fn(), delay, limitDelay, onLimited });
    const got = await lastValueFrom(run(link).pipe(toArray()));
    expect(got).toEqual([event]);
    expect(limitDelay.mock.calls).toEqual([[0], [1]]);
    expect(delay).not.toHaveBeenCalled();
    expect(onLimited.mock.calls).toEqual([[true], [false]]);
  });

  it("clears the limited state when the subscription is dropped", () => {
    const client = fakeClient([
      (sink) => {
        sink.error(new NetworkError(response(429)));
      },
    ]);
    const onLimited = vi.fn();
    const link = createSSELink({
      client,
      onAuthLost: vi.fn(),
      limitDelay: () => 60_000,
      onLimited,
    });
    const sub = run(link).subscribe(() => undefined);
    return new Promise<void>((resolve) => {
      queueMicrotask(() => {
        queueMicrotask(() => {
          expect(onLimited.mock.calls).toEqual([[true]]);
          sub.unsubscribe();
          expect(onLimited.mock.calls).toEqual([[true], [false]]);
          resolve();
        });
      });
    });
  });
});

/** Two tabs: hubs on one in-memory channel. */
function channelPair(): [ChannelLike, ChannelLike] {
  const listeners = [new Set<(ev: MessageEvent) => void>(), new Set<(ev: MessageEvent) => void>()];
  const make = (self: 0 | 1): ChannelLike => ({
    postMessage(message) {
      const data: unknown = structuredClone(message);
      const other = listeners[self === 0 ? 1 : 0] ?? new Set();
      queueMicrotask(() => {
        for (const l of [...other]) l(new MessageEvent("message", { data }));
      });
    },
    addEventListener(_t, l) {
      listeners[self]?.add(l);
    },
    removeEventListener(_t, l) {
      listeners[self]?.delete(l);
    },
    close() {
      listeners[self]?.clear();
    },
  });
  return [make(0), make(1)];
}

describe("shared stream link", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  function hubs() {
    const [ca, cb] = channelPair();
    const opts = { electionMs: 10, electionJitterMs: 0, lifecycle: null };
    return [
      new SharedStreams({ ...opts, channel: ca, tabId: "a" }),
      new SharedStreams({ ...opts, channel: cb, tabId: "b" }),
    ] as const;
  }

  /** A terminating link whose stream the test drives. */
  function source() {
    const sinks: { next: (r: FormattedExecutionResult) => void; complete: () => void }[] = [];
    let opened = 0;
    const link = new ApolloLink(
      () =>
        new Observable<FormattedExecutionResult>((observer) => {
          opened++;
          sinks.push({
            next: (r) => {
              observer.next(r);
            },
            complete: () => {
              observer.complete();
            },
          });
        }),
    );
    return { link, sinks, opened: () => opened };
  }

  function runShared(link: ApolloLink, key = "account:u1") {
    return ApolloLink.execute(
      link,
      { query: SUB, context: sharedStream(key) },
      { client: {} as never },
    );
  }

  it("opens one stream for two tabs and relays its events", async () => {
    vi.useFakeTimers();
    const [ha, hb] = hubs();
    const sa = source();
    const sb = source();
    const la = ApolloLink.from([createSharedStreamLink({ hub: ha, onAuthLost: vi.fn() }), sa.link]);
    const lb = ApolloLink.from([createSharedStreamLink({ hub: hb, onAuthLost: vi.fn() }), sb.link]);
    const gotA: unknown[] = [];
    const gotB: unknown[] = [];
    runShared(la).subscribe((r) => gotA.push(r));
    await vi.advanceTimersByTimeAsync(20);
    runShared(lb).subscribe((r) => gotB.push(r));
    await vi.advanceTimersByTimeAsync(20);
    expect(sa.opened()).toBe(1);
    expect(sb.opened()).toBe(0);
    sa.sinks[0]?.next(event);
    await vi.advanceTimersByTimeAsync(0);
    expect(gotA).toEqual([event]);
    expect(gotB).toEqual([event]);
  });

  it("runs onAuthLost in a follower when the relayed stream ended the session", async () => {
    vi.useFakeTimers();
    const [ha, hb] = hubs();
    const sa = source();
    const lostA = vi.fn();
    const lostB = vi.fn();
    const la = ApolloLink.from([createSharedStreamLink({ hub: ha, onAuthLost: lostA }), sa.link]);
    const lb = ApolloLink.from([
      createSharedStreamLink({ hub: hb, onAuthLost: lostB }),
      source().link,
    ]);
    let completedB = false;
    runShared(la).subscribe(() => undefined);
    await vi.advanceTimersByTimeAsync(20);
    runShared(lb).subscribe({
      complete: () => {
        completedB = true;
      },
    });
    await vi.advanceTimersByTimeAsync(20);
    sa.sinks[0]?.next(endResult("UNAUTHENTICATED"));
    sa.sinks[0]?.complete();
    await vi.advanceTimersByTimeAsync(0);
    // The leader's SSE link handles its own tab; the relay covers the other.
    expect(lostA).not.toHaveBeenCalled();
    expect(lostB).toHaveBeenCalledTimes(1);
    expect(completedB).toBe(true);
  });

  it("passes unshared subscriptions and missing hubs straight through", async () => {
    const s1 = source();
    const plain = ApolloLink.from([
      createSharedStreamLink({ hub: null, onAuthLost: vi.fn() }),
      s1.link,
    ]);
    runShared(plain).subscribe(() => undefined);
    expect(s1.opened()).toBe(1);
    const [ha] = hubs();
    const s2 = source();
    const unshared = ApolloLink.from([
      createSharedStreamLink({ hub: ha, onAuthLost: vi.fn() }),
      s2.link,
    ]);
    run(unshared).subscribe(() => undefined);
    await Promise.resolve();
    expect(s2.opened()).toBe(1);
  });
});
