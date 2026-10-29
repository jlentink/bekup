/** A key bekup generated and returns exactly once (D-142). */
export interface OneTimeKey {
  kind: "passphrase" | "identity";
  /** The passphrase or the AGE-SECRET-KEY-1... identity. */
  value: string;
  version?: number;
  /** The age public key (identity only), for reference. */
  recipient?: string;
  /** Downloadable file: the age-keygen identity file, or the passphrase as text. */
  file: { name: string; content: string };
}

/** The one-time view of a generated passphrase. */
export function passphraseKey(jobSlug: string, passphrase: string, version?: number): OneTimeKey {
  return {
    kind: "passphrase",
    value: passphrase,
    ...(version != null ? { version } : {}),
    file: {
      name: `${jobSlug}-passphrase${version != null ? `-v${String(version)}` : ""}.txt`,
      content: `${passphrase}\n`,
    },
  };
}

/** The one-time view of a generated age keypair. */
export function identityKey(k: {
  identity: string;
  identityFile: string;
  fileName: string;
  recipient: string;
  version: number;
}): OneTimeKey {
  return {
    kind: "identity",
    value: k.identity,
    version: k.version,
    recipient: k.recipient,
    file: { name: k.fileName, content: k.identityFile },
  };
}
