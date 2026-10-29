import { vi } from "vitest";

/**
 * Makes WebAuthn available in jsdom: a secure context, a
 * PublicKeyCredential constructor and navigator.credentials with the given
 * mocks. Returns the function that removes the stub.
 */
export function stubWebAuthn(credentials: Partial<CredentialsContainer>): () => void {
  vi.stubGlobal("isSecureContext", true);
  vi.stubGlobal("PublicKeyCredential", vi.fn());
  Object.defineProperty(navigator, "credentials", { value: credentials, configurable: true });
  return () => {
    Reflect.deleteProperty(navigator, "credentials");
  };
}

/** Bytes as an ArrayBuffer. */
export function bytes(...b: number[]): ArrayBuffer {
  return new Uint8Array(b).buffer;
}
