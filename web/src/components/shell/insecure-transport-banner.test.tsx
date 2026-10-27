import type { MockLink } from "@apollo/client/testing";
import { MockedProvider } from "@apollo/client/testing/react";
import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { pageIsInsecure, ServerInfoQuery } from "@/lib/server-info";

import { InsecureTransportBanner } from "./insecure-transport-banner";

function serverInfo(insecureTransport: boolean): MockLink.MockedResponse {
  return {
    request: { query: ServerInfoQuery },
    result: {
      data: {
        serverInfo: {
          __typename: "ServerInfo",
          version: "1.0.0",
          insecureTransport,
          devMode: false,
        },
      },
    },
  };
}

function renderBanner(mocks: MockLink.MockedResponse[]) {
  return render(
    <MockedProvider mocks={mocks}>
      <InsecureTransportBanner />
    </MockedProvider>,
  );
}

describe("pageIsInsecure", () => {
  it("is true for anything but https", () => {
    expect(pageIsInsecure({ protocol: "http:" })).toBe(true);
    expect(pageIsInsecure({ protocol: "https:" })).toBe(false);
  });
});

describe("InsecureTransportBanner", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("warns when the server reports insecure transport", async () => {
    vi.stubGlobal("location", { protocol: "https:" });
    renderBanner([serverInfo(true)]);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Connection is not encrypted (no HTTPS)",
    );
  });

  it("stays hidden over https when the server is secure", async () => {
    vi.stubGlobal("location", { protocol: "https:" });
    renderBanner([serverInfo(false)]);
    // Let the mocked query resolve.
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("warns over plain http even without a server answer", () => {
    vi.stubGlobal("location", { protocol: "http:" });
    renderBanner([]);
    expect(screen.getByRole("alert")).toHaveTextContent("Not encrypted");
  });
});
