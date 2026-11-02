/**
 * The browser setup's API (D-217, internal/setup/web). Every state-changing
 * request carries the marker header; the session cookie is HttpOnly. Secret
 * answers travel as raw bytes, never inside JSON.
 */

export type StepKind = "note" | "sensitive" | "select" | "input" | "secret" | "confirm";

export interface StepOption {
  value: string;
  label: string;
}

export interface Step {
  id: number;
  kind: StepKind;
  title: string;
  body: string;
  options?: StepOption[];
  default?: string;
  error?: string;
}

export type Phase = "questions" | "done" | "failed";

export interface Status {
  phase: Phase;
  step?: Step;
  report: string;
  error?: string;
  zip: boolean;
}

export class SetupApiError extends Error {
  readonly status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

const MARKER = { "X-Bekup-Setup": "1" };

async function fail(res: Response): Promise<never> {
  let message = `request failed (${res.status})`;
  try {
    const body = (await res.json()) as { error?: string };
    if (body.error) message = body.error;
  } catch {
    /* not JSON */
  }
  throw new SetupApiError(res.status, message);
}

async function post(path: string, body: BodyInit, contentType: string): Promise<Response> {
  const res = await fetch(path, {
    method: "POST",
    headers: { ...MARKER, "Content-Type": contentType },
    body,
    credentials: "same-origin",
  });
  if (!res.ok) await fail(res);
  return res;
}

export async function signIn(code: string): Promise<void> {
  await post("/api/setup/session", JSON.stringify({ code }), "application/json");
}

/** status waits (long poll) for a step other than `after`. */
export async function status(after?: number, signal?: AbortSignal): Promise<Status> {
  const q = after ? `?after=${after}` : "";
  const res = await fetch(`/api/setup/status${q}`, {
    credentials: "same-origin",
    signal: signal ?? null,
  });
  if (!res.ok) await fail(res);
  return (await res.json()) as Status;
}

export async function answer(step: number, value: string): Promise<void> {
  await post("/api/setup/answer", JSON.stringify({ step, value }), "application/json");
}

export async function back(step: number): Promise<void> {
  await post("/api/setup/answer", JSON.stringify({ step, back: true }), "application/json");
}

/** answerSecret sends the secret as bytes; the caller clears its copy. */
export async function answerSecret(step: number, secret: Uint8Array): Promise<void> {
  await post(`/api/setup/secret?step=${step}`, secret as BodyInit, "application/octet-stream");
}

/** reveal fetches the show-once passphrase of a sensitive step. */
export async function reveal(step: number): Promise<string> {
  const res = await post("/api/setup/reveal", JSON.stringify({ step }), "application/json");
  return res.text();
}

export async function finish(): Promise<void> {
  await post("/api/setup/finish", "{}", "application/json");
}

export const zipURL = "/api/setup/config.zip";
