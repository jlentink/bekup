import { z } from "zod";

import type {
  ConnectionDetailQuery,
  ConnectionTlsMode,
  CreateConnectionInput,
  DatabaseEngine,
  UpdateConnectionInput,
} from "@/graphql/gen/graphql";

import { defaultPort } from "./labels";

/**
 * The connection form model: values, validation (incl. the D-140 rule that
 * a stored password is never sent to a new endpoint) and the mapping to
 * the create/update inputs. Pure functions, unit tested.
 */

export type Original = NonNullable<ConnectionDetailQuery["connection"]>;

export type SecretAction = "keep" | "set" | "clear";
export type SshAuthChoice = "keep" | "password" | "privateKey" | "later";
export type ClientCertAction = "none" | "keep" | "set" | "clear";

export interface ConnectionFormValues {
  name: string;
  engine: DatabaseEngine;
  versionKey: string;
  /** "" uses the version key. */
  imageKey: string;
  host: string;
  /** "" uses the engine default (create) or keeps the port (edit). */
  port: string;
  username: string;
  passwordAction: SecretAction;
  password: string;
  tlsMode: ConnectionTlsMode;
  clientCert: ClientCertAction;
  clientCertificatePem: string;
  clientKeyPem: string;
  sshEnabled: boolean;
  ssh: {
    host: string;
    port: string;
    user: string;
    auth: SshAuthChoice;
    password: string;
    privateKey: string;
    privateKeyPassphrase: string;
  };
  /** "" uses the global default. */
  maxConcurrentRuns: string;
}

/** Server field names (VALIDATION extensions.field) → form fields. */
export const SERVER_FIELDS = {
  name: "name",
  engine: "engine",
  versionKey: "versionKey",
  imageKey: "imageKey",
  host: "host",
  port: "port",
  username: "username",
  password: "password",
  tlsMode: "tlsMode",
  clientCertificatePem: "clientCertificatePem",
  clientKeyPem: "clientKeyPem",
  maxConcurrentRuns: "maxConcurrentRuns",
  ssh: "ssh.host",
  "ssh.host": "ssh.host",
  "ssh.port": "ssh.port",
  "ssh.user": "ssh.user",
  "ssh.password": "ssh.password",
  "ssh.privateKey": "ssh.privateKey",
  "ssh.privateKeyPassphrase": "ssh.privateKeyPassphrase",
} as const;

export type FormField = (typeof SERVER_FIELDS)[keyof typeof SERVER_FIELDS];
export const FORM_FIELDS: readonly FormField[] = [...new Set(Object.values(SERVER_FIELDS))];

export function emptyValues(): ConnectionFormValues {
  return {
    name: "",
    engine: "POSTGRES",
    versionKey: "",
    imageKey: "",
    host: "",
    port: "",
    username: "",
    passwordAction: "set",
    password: "",
    tlsMode: "PINNED",
    clientCert: "none",
    clientCertificatePem: "",
    clientKeyPem: "",
    sshEnabled: false,
    ssh: {
      host: "",
      port: "",
      user: "",
      auth: "later",
      password: "",
      privateKey: "",
      privateKeyPassphrase: "",
    },
    maxConcurrentRuns: "",
  };
}

export function valuesFrom(c: Original): ConnectionFormValues {
  const s = c.settings;
  const ssh = s?.ssh;
  return {
    name: c.name,
    engine: c.engine,
    versionKey: c.versionKey,
    imageKey: s && s.imageKey !== c.versionKey ? s.imageKey : "",
    host: c.host,
    port: String(c.port),
    username: s?.username ?? "",
    passwordAction: s?.hasPassword ? "keep" : "set",
    password: "",
    tlsMode: c.tlsMode,
    clientCert: s?.clientCertificatePem ? "keep" : "none",
    clientCertificatePem: "",
    clientKeyPem: "",
    sshEnabled: c.sshEnabled,
    ssh: {
      host: ssh?.host ?? "",
      port: ssh ? String(ssh.port) : "",
      user: ssh?.user ?? "",
      auth: ssh && ssh.authMethod !== "NONE" ? "keep" : "later",
      password: "",
      privateKey: "",
      privateKeyPassphrase: "",
    },
    maxConcurrentRuns: c.maxConcurrentRuns == null ? "" : String(c.maxConcurrentRuns),
  };
}

function portOf(v: string, fallback: number): number {
  return v.trim() === "" ? fallback : Number(v);
}

/** What changed about the endpoints, relative to the stored connection (D-140). */
export function retarget(v: ConnectionFormValues, orig: Original | undefined) {
  if (!orig)
    return { dbMoved: false, sshMoved: false, needsPassword: false, needsSshPassword: false };
  const ssh = orig.settings?.ssh;
  const bastionMoved =
    orig.sshEnabled &&
    v.sshEnabled &&
    !!ssh &&
    (v.ssh.host.trim() !== ssh.host || portOf(v.ssh.port, 22) !== ssh.port);
  const dbMoved =
    v.host.trim() !== orig.host ||
    portOf(v.port, orig.port) !== orig.port ||
    v.sshEnabled !== orig.sshEnabled ||
    bastionMoved;
  const sshMoved =
    bastionMoved || (orig.sshEnabled && v.sshEnabled && !!ssh && v.ssh.user.trim() !== ssh.user);
  return {
    dbMoved,
    sshMoved,
    needsPassword: dbMoved && orig.settings?.hasPassword === true,
    needsSshPassword: sshMoved && ssh.authMethod === "PASSWORD",
  };
}

/** The password action that applies: a required re-entry turns "keep" into "set". */
export function effectivePasswordAction(
  v: ConnectionFormValues,
  orig: Original | undefined,
): SecretAction {
  return v.passwordAction === "keep" && retarget(v, orig).needsPassword ? "set" : v.passwordAction;
}

/** The SSH credential choice that applies after a bastion change. */
export function effectiveSshAuth(
  v: ConnectionFormValues,
  orig: Original | undefined,
): SshAuthChoice {
  if (!v.sshEnabled) return "later";
  if (orig && !orig.sshEnabled && v.ssh.auth === "keep") return "later";
  return v.ssh.auth === "keep" && retarget(v, orig).needsSshPassword ? "password" : v.ssh.auth;
}

const PORT_RE = /^\d{1,5}$/;
const PEM_CERT = "-----BEGIN CERTIFICATE-----";

export function buildSchema(orig: Original | undefined) {
  const str = z.string();
  return z
    .object({
      name: str.trim().min(1, "Enter a name.").max(100, "At most 100 characters."),
      engine: z.enum(["POSTGRES", "MYSQL", "MARIADB"]),
      versionKey: str.min(1, "Choose the server's version."),
      imageKey: str,
      host: str.trim().min(1, "Enter the database host."),
      port: str.trim(),
      username: str.trim().min(1, "Enter the database user."),
      passwordAction: z.enum(["keep", "set", "clear"]),
      password: str,
      tlsMode: z.enum(["PINNED", "UNVERIFIED", "DISABLED"]),
      clientCert: z.enum(["none", "keep", "set", "clear"]),
      clientCertificatePem: str,
      clientKeyPem: str,
      sshEnabled: z.boolean(),
      ssh: z.object({
        host: str.trim(),
        port: str.trim(),
        user: str.trim(),
        auth: z.enum(["keep", "password", "privateKey", "later"]),
        password: str,
        privateKey: str,
        privateKeyPassphrase: str,
      }),
      maxConcurrentRuns: str.trim(),
    })
    .superRefine((v, ctx) => {
      const issue = (path: (string | number)[], message: string) => {
        ctx.addIssue({ code: "custom", path, message });
      };
      const badPort = (p: string) =>
        p !== "" && (!PORT_RE.test(p) || Number(p) < 1 || Number(p) > 65535);
      if (badPort(v.port)) issue(["port"], "Enter a port between 1 and 65535.");
      if (v.maxConcurrentRuns !== "") {
        const n = Number(v.maxConcurrentRuns);
        if (!Number.isInteger(n) || n < 1 || n > 64)
          issue(["maxConcurrentRuns"], "Enter a whole number from 1 to 64, or leave it empty.");
      }
      const values: ConnectionFormValues = v;
      const pw = effectivePasswordAction(values, orig);
      if (pw === "set" && orig && v.password === "") {
        issue(
          ["password"],
          retarget(values, orig).needsPassword
            ? "The endpoint changed: enter the password again, or remove it. A stored password is never sent to a new endpoint."
            : "Enter the new password.",
        );
      }
      if (v.clientCert === "set") {
        if (!v.clientCertificatePem.trim().startsWith(PEM_CERT))
          issue(["clientCertificatePem"], "Paste the client certificate as PEM.");
        if (v.clientKeyPem.trim() === "")
          issue(["clientKeyPem"], "Paste the matching private key (PEM).");
      }
      if (v.sshEnabled) {
        if (v.ssh.host === "") issue(["ssh", "host"], "Enter the bastion host.");
        if (badPort(v.ssh.port)) issue(["ssh", "port"], "Enter a port between 1 and 65535.");
        if (v.ssh.user === "") issue(["ssh", "user"], "Enter the SSH user.");
        const auth = effectiveSshAuth(values, orig);
        if (auth === "password" && v.ssh.password === "")
          issue(
            ["ssh", "password"],
            retarget(values, orig).needsSshPassword
              ? "The bastion or user changed: enter the SSH password again, or set a key. A stored password is never sent to a new endpoint."
              : "Enter the SSH password.",
          );
        if (auth === "privateKey" && !v.ssh.privateKey.includes("PRIVATE KEY"))
          issue(["ssh", "privateKey"], "Paste the private key (PEM or OpenSSH format).");
      }
    });
}

function sshInput(v: ConnectionFormValues, orig: Original | undefined) {
  const auth = effectiveSshAuth(v, orig);
  return {
    host: v.ssh.host.trim(),
    ...(v.ssh.port !== "" ? { port: Number(v.ssh.port) } : {}),
    user: v.ssh.user.trim(),
    ...(auth === "password" ? { password: v.ssh.password } : {}),
    ...(auth === "privateKey"
      ? {
          privateKey: v.ssh.privateKey,
          ...(v.ssh.privateKeyPassphrase !== ""
            ? { privateKeyPassphrase: v.ssh.privateKeyPassphrase }
            : {}),
        }
      : {}),
  };
}

export function toCreateInput(v: ConnectionFormValues): CreateConnectionInput {
  return {
    name: v.name.trim(),
    engine: v.engine,
    versionKey: v.versionKey,
    ...(v.imageKey !== "" ? { imageKey: v.imageKey } : {}),
    host: v.host.trim(),
    ...(v.port !== "" ? { port: Number(v.port) } : {}),
    username: v.username.trim(),
    ...(v.password !== "" ? { password: v.password } : {}),
    tlsMode: v.tlsMode,
    ...(v.clientCert === "set"
      ? { clientCertificatePem: v.clientCertificatePem.trim(), clientKeyPem: v.clientKeyPem }
      : {}),
    ...(v.sshEnabled ? { ssh: sshInput(v, undefined) } : {}),
    ...(v.maxConcurrentRuns !== "" ? { maxConcurrentRuns: Number(v.maxConcurrentRuns) } : {}),
  };
}

/** Only the changed fields; credentials only when entered (write-only). */
export function toUpdateInput(v: ConnectionFormValues, orig: Original): UpdateConnectionInput {
  const s = orig.settings;
  const input: UpdateConnectionInput = {};
  if (v.name.trim() !== orig.name) input.name = v.name.trim();
  if (v.versionKey !== orig.versionKey) input.versionKey = v.versionKey;
  const image = v.imageKey !== "" ? v.imageKey : v.versionKey;
  if (s && image !== s.imageKey) input.imageKey = image;
  if (v.host.trim() !== orig.host) input.host = v.host.trim();
  if (v.port !== "" && Number(v.port) !== orig.port) input.port = Number(v.port);
  if (s && v.username.trim() !== s.username) input.username = v.username.trim();
  const pw = effectivePasswordAction(v, orig);
  if (pw === "set" && v.password !== "") input.password = v.password;
  if (pw === "clear" && s?.hasPassword) input.clearPassword = true;
  if (v.clientCert === "set") {
    input.clientCertificatePem = v.clientCertificatePem.trim();
    input.clientKeyPem = v.clientKeyPem;
  }
  if (v.clientCert === "clear") input.clearClientCertificate = true;
  if (v.sshEnabled) {
    const ssh = s?.ssh;
    const changed =
      !orig.sshEnabled ||
      v.ssh.host.trim() !== ssh?.host ||
      portOf(v.ssh.port, 22) !== ssh.port ||
      v.ssh.user.trim() !== ssh.user ||
      effectiveSshAuth(v, orig) === "password" ||
      effectiveSshAuth(v, orig) === "privateKey";
    if (changed) input.ssh = sshInput(v, orig);
  } else if (orig.sshEnabled) {
    input.disableSsh = true;
  }
  if (v.maxConcurrentRuns === "" && orig.maxConcurrentRuns != null) {
    input.clearMaxConcurrentRuns = true;
  } else if (v.maxConcurrentRuns !== "" && Number(v.maxConcurrentRuns) !== orig.maxConcurrentRuns) {
    input.maxConcurrentRuns = Number(v.maxConcurrentRuns);
  }
  return input;
}

export { defaultPort };
