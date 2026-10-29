import { afterEach, describe, expect, it, vi } from "vitest";

import {
  base64urlToBuffer,
  bufferToBase64url,
  createCredential,
  decodeCreationOptions,
  decodeRequestOptions,
  getAssertion,
  WebAuthnError,
  webauthnSupported,
} from "./webauthn";
import { bytes, stubWebAuthn } from "@/test/webauthn";

let restore: (() => void) | undefined;

function enableWebAuthn(credentials: Partial<CredentialsContainer>) {
  restore = stubWebAuthn(credentials);
}

afterEach(() => {
  restore?.();
  restore = undefined;
  vi.unstubAllGlobals();
});

describe("base64url", () => {
  it("round-trips bytes without padding", () => {
    const buf = bytes(0xfb, 0xff, 0x00, 0x10);
    expect(bufferToBase64url(buf)).toBe("-_8AEA");
    expect(new Uint8Array(base64urlToBuffer("-_8AEA"))).toEqual(new Uint8Array(buf));
    expect(new Uint8Array(base64urlToBuffer("-_8AEA=="))).toEqual(new Uint8Array(buf));
  });

  it("rejects other alphabets", () => {
    expect(() => base64urlToBuffer("a+b/")).toThrow(WebAuthnError);
  });
});

describe("options", () => {
  it("decodes request options", () => {
    const o = decodeRequestOptions({
      publicKey: {
        challenge: "AAEC",
        rpId: "bekup.example.com",
        allowCredentials: [{ type: "public-key", id: "AQI", transports: ["usb"] }],
        userVerification: "preferred",
      },
    });
    expect(new Uint8Array(o.challenge as ArrayBuffer)).toEqual(new Uint8Array([0, 1, 2]));
    expect(o.rpId).toBe("bekup.example.com");
    expect(o.allowCredentials?.[0]?.transports).toEqual(["usb"]);
  });

  it("decodes creation options with the user handle", () => {
    const o = decodeCreationOptions({
      publicKey: {
        challenge: "AAEC",
        rp: { id: "localhost", name: "bekup" },
        user: { id: "BwgJ", name: "ada@example.com", displayName: "Ada" },
        pubKeyCredParams: [{ type: "public-key", alg: -7 }],
        excludeCredentials: [{ type: "public-key", id: "AQI" }],
      },
    });
    expect(new Uint8Array(o.user.id as ArrayBuffer)).toEqual(new Uint8Array([7, 8, 9]));
    expect(o.excludeCredentials).toHaveLength(1);
  });

  it("rejects malformed options", () => {
    expect(() => decodeRequestOptions({ challenge: "AAEC" })).toThrow(WebAuthnError);
    expect(() => decodeCreationOptions({ publicKey: { challenge: "AA" } })).toThrow(WebAuthnError);
  });
});

describe("ceremonies", () => {
  it("is unsupported outside a secure context", async () => {
    vi.stubGlobal("isSecureContext", false);
    expect(webauthnSupported()).toBe(false);
    await expect(getAssertion({ publicKey: { challenge: "AA" } })).rejects.toMatchObject({
      kind: "unsupported",
    });
  });

  it("encodes an attestation for the server", async () => {
    const create = vi.fn().mockResolvedValue({
      id: "AQI",
      type: "public-key",
      rawId: bytes(1, 2),
      authenticatorAttachment: null,
      getClientExtensionResults: () => ({ credProps: { rk: true } }),
      response: {
        clientDataJSON: bytes(3),
        attestationObject: bytes(6),
        getTransports: () => ["internal"],
      },
    });
    enableWebAuthn({ create, get: vi.fn() });
    const json = await createCredential({
      publicKey: {
        challenge: "AAEC",
        rp: { name: "bekup" },
        user: { id: "BwgJ", name: "a", displayName: "a" },
      },
    });
    expect(json).toEqual({
      id: "AQI",
      rawId: "AQI",
      type: "public-key",
      authenticatorAttachment: undefined,
      clientExtensionResults: { credProps: { rk: true } },
      response: { clientDataJSON: "Aw", attestationObject: "Bg", transports: ["internal"] },
    });
  });

  it("reports a cancelled ceremony", async () => {
    const get = vi.fn().mockRejectedValue(new DOMException("denied", "NotAllowedError"));
    enableWebAuthn({ get, create: vi.fn() });
    await expect(getAssertion({ publicKey: { challenge: "AA" } })).rejects.toMatchObject({
      kind: "cancelled",
    });
  });
});
