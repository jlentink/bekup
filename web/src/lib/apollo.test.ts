import { describe, expect, it, vi } from "vitest";

import { graphqlWsUrl, redirectToLogin, WS_CLOSE, wsRequiresLogin, wsShouldRetry } from "./apollo";

describe("graphqlWsUrl", () => {
  it("uses wss: on https pages and ws: otherwise, same host", () => {
    expect(graphqlWsUrl({ protocol: "https:", host: "bekup.example.com" })).toBe(
      "wss://bekup.example.com/graphql",
    );
    expect(graphqlWsUrl({ protocol: "http:", host: "127.0.0.1:8080" })).toBe(
      "ws://127.0.0.1:8080/graphql",
    );
  });
});

describe("WebSocket close handling", () => {
  it("requires a login after 4401 and 4403 only", () => {
    expect(wsRequiresLogin({ code: WS_CLOSE.sessionEnded, reason: "" })).toBe(true);
    expect(wsRequiresLogin({ code: WS_CLOSE.accessChanged, reason: "" })).toBe(true);
    expect(wsRequiresLogin({ code: WS_CLOSE.sessionRotated, reason: "" })).toBe(false);
    expect(wsRequiresLogin({ code: 1013, reason: "" })).toBe(false);
    expect(wsRequiresLogin(new Error("network"))).toBe(false);
  });

  it("retries rotations and transient closes but not lost sessions", () => {
    expect(wsShouldRetry({ code: WS_CLOSE.sessionRotated, reason: "" })).toBe(true);
    expect(wsShouldRetry({ code: 1013, reason: "" })).toBe(true);
    expect(wsShouldRetry({ code: WS_CLOSE.sessionEnded, reason: "" })).toBe(false);
    expect(wsShouldRetry({ code: WS_CLOSE.accessChanged, reason: "" })).toBe(false);
    expect(wsShouldRetry({ code: WS_CLOSE.badInit, reason: "" })).toBe(false);
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
