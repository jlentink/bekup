import { ApolloLink, gql } from "@apollo/client";
import type { FormattedExecutionResult } from "graphql";
import { NetworkError, type RequestParams, type Sink } from "graphql-sse";
import { firstValueFrom, lastValueFrom, toArray } from "rxjs";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  createSSELink,
  redirectToLogin,
  retryDelay,
  STREAM_END,
  streamEndCode,
  streamRequiresLogin,
  streamShouldRetry,
} from "./apollo";

const SUB = gql`
  subscription Sessions {
    sessionEvents {
      kind
    }
  }
`;

function endResult(code: string): FormattedExecutionResult {
  return { errors: [{ message: "ended", extensions: { code } }] };
}

const event: FormattedExecutionResult = { data: { sessionEvents: { kind: "REVOKED" } } };

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
