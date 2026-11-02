import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Status, Step, StepKind } from "./api";
import { T } from "./messages";

const CODE = "MFRGGZDFMZTWQ2LKNNWG23TPOA";
const PASSPHRASE = "violet anchor quarry ribbon tundra";

interface Screen {
  kind: StepKind;
  title: string;
  body?: string;
  options?: { value: string; label: string }[];
  default?: string;
  /** validate returns the server's refusal for a value ("" accepts). */
  validate?: (value: string) => string;
}

interface Call {
  method: string;
  path: string;
  headers: Headers;
  body: string;
}

/**
 * FakeSetup is the setup server's API (internal/setup/web) for one flow:
 * the screens in order, Back to the previous one, refused answers
 * published again with the error, long polling on ?after=.
 */
class FakeSetup {
  calls: Call[] = [];
  signedIn = false;
  revealed = false;
  finished = false;
  private index = 0;
  private id = 1;
  private error = "";
  private waiters: (() => void)[] = [];

  private readonly screens: Screen[];

  constructor(screens: Screen[]) {
    this.screens = screens;
  }

  status(): Status {
    if (this.index >= this.screens.length) {
      return { phase: "done", report: "== setup check ==\nOK\n", zip: true };
    }
    const s = this.screens[this.index];
    if (!s) throw new Error("no screen");
    const step: Step = { id: this.id, kind: s.kind, title: s.title, body: s.body ?? "" };
    if (s.options) step.options = s.options;
    if (s.default) step.default = s.default;
    if (this.error) step.error = this.error;
    return { phase: "questions", step, report: "", zip: false };
  }

  private move(next: number, error = "") {
    this.index = next;
    this.error = error;
    this.id++;
    this.revealed = false;
    const w = this.waiters;
    this.waiters = [];
    w.forEach((f) => {
      f();
    });
  }

  private json(status: number, body?: unknown): Response {
    if (body === undefined) return new Response(null, { status });
    return new Response(JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
    });
  }

  fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = new URL(
      typeof input === "string" ? input : input instanceof URL ? input.href : input.url,
      "https://setup.example:8443",
    );
    const method = init?.method ?? "GET";
    const headers = new Headers(init?.headers);
    let body = "";
    if (typeof init?.body === "string") body = init.body;
    else if (ArrayBuffer.isView(init?.body)) body = new TextDecoder().decode(init.body);
    this.calls.push({ method, path: url.pathname + url.search, headers, body });
    if (method === "POST" && headers.get("X-Bekup-Setup") !== "1") {
      return this.json(403, { error: "request refused" });
    }
    if (url.pathname === "/api/setup/session") {
      const { code } = JSON.parse(body) as { code: string };
      if (code.replace(/[\s-]/g, "").toUpperCase() !== CODE || this.signedIn) {
        return this.json(401, { error: "wrong or used access code" });
      }
      this.signedIn = true;
      return this.json(204);
    }
    if (!this.signedIn) return this.json(401, { error: "sign in with the access code" });
    const cur = this.screens[this.index];
    switch (url.pathname) {
      case "/api/setup/status": {
        const after = url.searchParams.get("after");
        const st = this.status();
        if (after !== null && st.phase === "questions" && Number(after) === st.step?.id) {
          await new Promise<void>((ok, fail) => {
            this.waiters.push(ok);
            init?.signal?.addEventListener("abort", () => {
              fail(new DOMException("aborted", "AbortError"));
            });
          });
        }
        return this.json(200, this.status());
      }
      case "/api/setup/answer": {
        const a = JSON.parse(body) as { step: number; back?: boolean; value?: string };
        if (!cur || a.step !== this.id)
          return this.json(409, { error: "this step was answered already: reload" });
        if (a.back) {
          this.move(Math.max(this.index - 1, 0));
          return this.json(204);
        }
        const refusal = cur.validate?.(a.value ?? "") ?? "";
        this.move(refusal ? this.index : this.index + 1, refusal);
        return this.json(204);
      }
      case "/api/setup/secret": {
        if (cur?.kind !== "secret" || url.searchParams.get("step") !== String(this.id)) {
          return this.json(409, { error: "the answer does not fit this step" });
        }
        this.move(body === "" ? Math.max(this.index - 1, 0) : this.index + 1);
        return this.json(204);
      }
      case "/api/setup/reveal": {
        if (cur?.kind !== "sensitive" || this.revealed) {
          return this.json(409, { error: "the passphrase was shown already" });
        }
        this.revealed = true;
        return new Response(PASSPHRASE, { status: 200, headers: { "Cache-Control": "no-store" } });
      }
      case "/api/setup/finish":
        this.finished = true;
        return this.json(204);
      default:
        return this.json(404, { error: "not found" });
    }
  };

  posted(path: string): Call[] {
    return this.calls.filter((c) => c.method === "POST" && c.path.startsWith(path));
  }
}

/** renderSetup loads the SPA at url (the module reads the fragment once). */
async function renderSetup(fake: FakeSetup, url = "/") {
  window.history.replaceState(null, "", url);
  vi.stubGlobal("fetch", fake.fetch);
  vi.resetModules();
  const { SetupApp } = await import("./app");
  render(<SetupApp />);
  return userEvent.setup();
}

const proxyOptions = [
  { value: "own-traefik", label: "bekup's own Traefik" },
  { value: "own-proxy", label: "your own reverse proxy" },
];

beforeEach(() => {
  window.history.replaceState(null, "", "/");
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("browser setup", () => {
  it("takes the code from the fragment, removes it from history and signs in", async () => {
    const fake = new FakeSetup([{ kind: "note", title: "bekup setup", body: "Welcome." }]);
    const replace = vi.spyOn(window.history, "replaceState");
    const user = await renderSetup(fake, `/#code=${CODE}`);
    expect(window.location.hash).toBe("");
    expect(window.location.href).not.toContain(CODE);
    expect(replace).toHaveBeenCalled();
    const input = await screen.findByLabelText(T.codeLabel);
    expect(input).toHaveValue(CODE);
    expect(screen.getByText(T.fingerprintCheck)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: T.signIn }));
    expect(await screen.findByRole("heading", { name: "bekup setup" })).toBeInTheDocument();
    const [login] = fake.posted("/api/setup/session");
    expect(login?.headers.get("X-Bekup-Setup")).toBe("1");
    expect(login?.headers.get("Content-Type")).toBe("application/json");
    expect(JSON.parse(login?.body ?? "{}")).toEqual({ code: CODE });
    // The code never travels in a URL.
    expect(fake.calls.some((c) => c.path.includes(CODE))).toBe(false);
  });

  it("shows a wrong code's error and keeps the form", async () => {
    const fake = new FakeSetup([{ kind: "note", title: "bekup setup" }]);
    const user = await renderSetup(fake);
    const input = await screen.findByLabelText(T.codeLabel);
    expect(input).toHaveValue("");
    expect(screen.getByRole("button", { name: T.signIn })).toBeDisabled();
    await user.type(input, "WRONGCODE");
    await user.click(screen.getByRole("button", { name: T.signIn }));
    expect(await screen.findByRole("alert")).toHaveTextContent("wrong or used access code");
    expect(input).toHaveAttribute("aria-invalid", "true");
  });

  it("shows a refused answer with the server's message, then goes Back", async () => {
    const fake = new FakeSetup([
      {
        kind: "select",
        title: "Reverse proxy",
        options: proxyOptions,
        default: "own-traefik",
      },
      {
        kind: "input",
        title: "Domain",
        body: "The public host name.",
        validate: (v) => (v.includes(".") ? "" : "not a fully qualified host name"),
      },
      { kind: "confirm", title: "Write the deployment now?", default: "yes" },
    ]);
    fake.signedIn = true;
    const user = await renderSetup(fake);
    expect(await screen.findByRole("heading", { name: "Reverse proxy" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: T.back })).not.toBeInTheDocument();
    await user.click(screen.getByRole("radio", { name: /your own reverse proxy/ }));
    await user.click(screen.getByRole("button", { name: T.continue }));

    const domain = await screen.findByRole("textbox", { name: "Domain" });
    const ledger = screen.getByRole("complementary", { name: T.ledgerHeading });
    expect(within(ledger).getByText("your own reverse proxy")).toBeInTheDocument();
    await user.type(domain, "backup");
    await user.click(screen.getByRole("button", { name: T.continue }));
    expect(await screen.findByRole("alert")).toHaveTextContent("not a fully qualified host name");
    expect(screen.getByRole("textbox", { name: "Domain" })).toHaveAttribute("aria-invalid", "true");
    // The refused value is not in the ledger.
    expect(within(ledger).queryByText("backup")).not.toBeInTheDocument();

    await user.type(screen.getByRole("textbox", { name: "Domain" }), ".example.com");
    await user.click(screen.getByRole("button", { name: T.continue }));
    expect(
      await screen.findByRole("heading", { name: "Write the deployment now?" }),
    ).toBeInTheDocument();
    expect(within(ledger).getAllByRole("listitem")).toHaveLength(2);

    await user.click(screen.getByRole("button", { name: T.back }));
    expect(await screen.findByRole("heading", { name: "Domain" })).toBeInTheDocument();
    expect(within(ledger).getAllByRole("listitem")).toHaveLength(1);
    const backs = fake.posted("/api/setup/answer").filter((c) => c.body.includes('"back":true'));
    expect(backs).toHaveLength(1);
  });

  it("sends a secret as raw bytes, never inside JSON", async () => {
    const fake = new FakeSetup([
      { kind: "secret", title: "Administrator password" },
      { kind: "note", title: "Done asking" },
    ]);
    fake.signedIn = true;
    const user = await renderSetup(fake);
    const field = await screen.findByLabelText("Administrator password", { selector: "input" });
    expect(field).toHaveAttribute("type", "password");
    await user.type(field, "s3cret-admin-pass");
    await user.click(screen.getByRole("button", { name: T.continue }));
    expect(await screen.findByRole("heading", { name: "Done asking" })).toBeInTheDocument();
    const [secret] = fake.posted("/api/setup/secret");
    expect(secret?.headers.get("Content-Type")).toBe("application/octet-stream");
    expect(secret?.body).toBe("s3cret-admin-pass");
    expect(fake.posted("/api/setup/answer").some((c) => c.body.includes("s3cret"))).toBe(false);
    expect(document.body.textContent).not.toContain("s3cret");
  });

  it("reveals the passphrase once and needs the acknowledgement", async () => {
    const fake = new FakeSetup([
      { kind: "sensitive", title: "CA key passphrase (shown ONCE)", body: "Keep it with the CA." },
    ]);
    fake.signedIn = true;
    const user = await renderSetup(fake);
    expect(
      await screen.findByRole("heading", { name: "CA key passphrase (shown ONCE)" }),
    ).toBeInTheDocument();
    const ack = screen.getByRole("button", { name: T.slipConfirm });
    expect(ack).toBeDisabled();
    expect(document.body.textContent).not.toContain(PASSPHRASE);
    await user.click(screen.getByRole("button", { name: T.slipReveal }));
    expect(await screen.findByText(PASSPHRASE)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: T.slipReveal })).not.toBeInTheDocument();
    expect(fake.posted("/api/setup/reveal")).toHaveLength(1);
    await user.click(ack);
    expect(await screen.findByRole("heading", { name: T.doneTitle })).toBeInTheDocument();
    expect(document.body.textContent).not.toContain(PASSPHRASE);
    const ledger = screen.getByRole("complementary", { name: T.ledgerHeading });
    expect(within(ledger).getByText(T.sensitiveShown)).toBeInTheDocument();
  });

  it("explains a passphrase that was revealed in another tab", async () => {
    const fake = new FakeSetup([{ kind: "sensitive", title: "CA key passphrase (shown ONCE)" }]);
    fake.signedIn = true;
    fake.revealed = true;
    const user = await renderSetup(fake);
    await user.click(await screen.findByRole("button", { name: T.slipReveal }));
    expect(await screen.findByText(T.slipGone)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: T.slipConfirm })).toBeEnabled();
  });

  it("ends with the report, the zip link and Finish", async () => {
    const fake = new FakeSetup([]);
    fake.signedIn = true;
    const user = await renderSetup(fake);
    expect(await screen.findByRole("heading", { name: T.doneTitle })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: T.reportHeading })).toHaveTextContent(
      "== setup check ==",
    );
    const zip = screen.getByRole("link", { name: T.zip });
    expect(zip).toHaveAttribute("href", "/api/setup/config.zip");
    expect(zip).toHaveAttribute("download");
    expect(screen.getByText(T.zipHint)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: T.finish }));
    expect(await screen.findByText(T.finished)).toBeInTheDocument();
    expect(fake.finished).toBe(true);
    await waitFor(() => {
      expect(screen.queryByRole("link", { name: T.zip })).not.toBeInTheDocument();
    });
  });

  it("tells when the setup server is gone", async () => {
    vi.stubGlobal("fetch", () => Promise.reject(new TypeError("Failed to fetch")));
    vi.resetModules();
    const { SetupApp } = await import("./app");
    render(<SetupApp />);
    expect(await screen.findByText(T.lost)).toBeInTheDocument();
  });
});
