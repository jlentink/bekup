/**
 * WebAuthn glue between the server's JSON (go-webauthn, base64url without
 * padding; delivered through the GraphQL JSON scalar as
 * `{"publicKey": {...}}`) and navigator.credentials. The conversion is done
 * here instead of PublicKeyCredential.parse*FromJSON so it behaves the
 * same in every supported browser.
 */

export type WebAuthnErrorKind = "unsupported" | "cancelled" | "invalid";

export class WebAuthnError extends Error {
  /** "unsupported": no WebAuthn here; "cancelled": the user or the timeout ended it; "invalid": bad options. */
  readonly kind: WebAuthnErrorKind;

  constructor(kind: WebAuthnErrorKind, message: string) {
    super(message);
    this.name = "WebAuthnError";
    this.kind = kind;
  }
}

/** True when this browser context can run WebAuthn ceremonies at all. */
export function webauthnSupported(): boolean {
  return (
    typeof window !== "undefined" &&
    window.isSecureContext &&
    typeof window.PublicKeyCredential === "function" &&
    // navigator.credentials only exists in secure contexts.
    "credentials" in navigator &&
    typeof navigator.credentials.get === "function"
  );
}

export function base64urlToBuffer(value: string): ArrayBuffer {
  if (!/^[A-Za-z0-9_-]*={0,2}$/.test(value)) {
    throw new WebAuthnError("invalid", "invalid base64url value");
  }
  const b64 = value.replace(/=+$/, "").replace(/-/g, "+").replace(/_/g, "/");
  const padded = b64 + "=".repeat((4 - (b64.length % 4)) % 4);
  const bin = atob(padded);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return bytes.buffer;
}

export function bufferToBase64url(buf: ArrayBuffer | ArrayBufferView): string {
  const bytes =
    buf instanceof ArrayBuffer
      ? new Uint8Array(buf)
      : new Uint8Array(buf.buffer, buf.byteOffset, buf.byteLength);
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

type JSONObject = Record<string, unknown>;

function isObject(v: unknown): v is JSONObject {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

/** The publicKey member of the server's options. */
function publicKeyOf(options: unknown): JSONObject {
  if (isObject(options) && isObject(options["publicKey"])) return options["publicKey"];
  throw new WebAuthnError("invalid", "passkey options are malformed");
}

function decodeDescriptors(list: unknown): PublicKeyCredentialDescriptor[] | undefined {
  if (!Array.isArray(list)) return undefined;
  return list.filter(isObject).map((d) => {
    const desc: PublicKeyCredentialDescriptor = {
      type: "public-key",
      id: base64urlToBuffer(String(d["id"])),
    };
    if (Array.isArray(d["transports"])) {
      desc.transports = d["transports"].map(String) as AuthenticatorTransport[];
    }
    return desc;
  });
}

/** Server JSON → PublicKeyCredentialRequestOptions. */
export function decodeRequestOptions(options: unknown): PublicKeyCredentialRequestOptions {
  const pk = publicKeyOf(options);
  const out: PublicKeyCredentialRequestOptions = {
    ...(pk as Partial<PublicKeyCredentialRequestOptions>),
    challenge: base64urlToBuffer(String(pk["challenge"])),
  };
  const allow = decodeDescriptors(pk["allowCredentials"]);
  if (allow) out.allowCredentials = allow;
  else delete out.allowCredentials;
  return out;
}

/** Server JSON → PublicKeyCredentialCreationOptions. */
export function decodeCreationOptions(options: unknown): PublicKeyCredentialCreationOptions {
  const pk = publicKeyOf(options);
  const user = pk["user"];
  if (!isObject(user)) throw new WebAuthnError("invalid", "passkey options are malformed");
  const out = {
    ...(pk as unknown as PublicKeyCredentialCreationOptions),
    challenge: base64urlToBuffer(String(pk["challenge"])),
    user: {
      ...(user as unknown as PublicKeyCredentialUserEntity),
      id: base64urlToBuffer(String(user["id"])),
    },
  };
  const exclude = decodeDescriptors(pk["excludeCredentials"]);
  if (exclude) out.excludeCredentials = exclude;
  else delete out.excludeCredentials;
  return out;
}

function encodeCommon(cred: PublicKeyCredential): JSONObject {
  return {
    id: cred.id,
    rawId: bufferToBase64url(cred.rawId),
    type: cred.type,
    authenticatorAttachment: cred.authenticatorAttachment ?? undefined,
    clientExtensionResults: cred.getClientExtensionResults(),
  };
}

/** An assertion → the JSON the server expects (PublicKeyCredential.toJSON shape). */
export function encodeAssertion(cred: PublicKeyCredential): JSONObject {
  const r = cred.response as AuthenticatorAssertionResponse;
  return {
    ...encodeCommon(cred),
    response: {
      clientDataJSON: bufferToBase64url(r.clientDataJSON),
      authenticatorData: bufferToBase64url(r.authenticatorData),
      signature: bufferToBase64url(r.signature),
      ...(r.userHandle ? { userHandle: bufferToBase64url(r.userHandle) } : {}),
    },
  };
}

/** An attestation → the JSON the server expects. */
export function encodeAttestation(cred: PublicKeyCredential): JSONObject {
  const r = cred.response as AuthenticatorAttestationResponse;
  const transports = typeof r.getTransports === "function" ? r.getTransports() : [];
  return {
    ...encodeCommon(cred),
    response: {
      clientDataJSON: bufferToBase64url(r.clientDataJSON),
      attestationObject: bufferToBase64url(r.attestationObject),
      transports,
    },
  };
}

function mapDomError(err: unknown): never {
  if (err instanceof WebAuthnError) throw err;
  if (
    err instanceof DOMException &&
    (err.name === "NotAllowedError" || err.name === "AbortError")
  ) {
    throw new WebAuthnError("cancelled", "the passkey check was cancelled");
  }
  if (err instanceof DOMException && err.name === "InvalidStateError") {
    throw new WebAuthnError("invalid", "this passkey is already registered");
  }
  if (err instanceof DOMException && err.name === "SecurityError") {
    throw new WebAuthnError("unsupported", "passkeys are not allowed for this address");
  }
  throw new WebAuthnError("cancelled", "the passkey check failed");
}

/** Runs navigator.credentials.get with the server's options; returns the JSON response. */
export async function getAssertion(options: unknown, signal?: AbortSignal): Promise<JSONObject> {
  if (!webauthnSupported())
    throw new WebAuthnError("unsupported", "passkeys are not supported here");
  const publicKey = decodeRequestOptions(options);
  let cred: Credential | null;
  try {
    cred = await navigator.credentials.get({ publicKey, ...(signal ? { signal } : {}) });
  } catch (err) {
    mapDomError(err);
  }
  if (cred?.type !== "public-key") throw new WebAuthnError("cancelled", "no passkey was used");
  return encodeAssertion(cred as PublicKeyCredential);
}

/** Runs navigator.credentials.create with the server's options; returns the JSON response. */
export async function createCredential(
  options: unknown,
  signal?: AbortSignal,
): Promise<JSONObject> {
  if (!webauthnSupported() || typeof navigator.credentials.create !== "function") {
    throw new WebAuthnError("unsupported", "passkeys are not supported here");
  }
  const publicKey = decodeCreationOptions(options);
  let cred: Credential | null;
  try {
    cred = await navigator.credentials.create({ publicKey, ...(signal ? { signal } : {}) });
  } catch (err) {
    mapDomError(err);
  }
  if (cred?.type !== "public-key") {
    throw new WebAuthnError("cancelled", "no passkey was created");
  }
  return encodeAttestation(cred as PublicKeyCredential);
}
