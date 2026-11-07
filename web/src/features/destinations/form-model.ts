import { z } from "zod";

import type {
  CreateDestinationInput,
  DestinationDetailQuery,
  DestinationType,
  ObjectLockMode,
  S3ChecksumMode,
  S3DestinationInput,
  S3Encryption,
  SftpDestinationInput,
  UpdateDestinationInput,
  WebdavDestinationInput,
  WebdavFlavor,
} from "@/graphql/gen/graphql";

import { defineMessages } from "@/lib/i18n";

import { WT } from "./webdav-text";

const E = defineMessages({
  en: {
    name: "Enter a name.",
    nameLength: "At most 100 characters.",
    diskPath: "Enter a directory below the disk root.",
    diskRelative: "Use a relative path without “..”.",
    s3Endpoint: "Enter an https:// URL, or leave it empty for AWS S3.",
    region: "Enter the region.",
    bucket: "Enter the bucket.",
    accessKeyId: "Enter the access key ID.",
    secretAccessKey: "Enter the secret access key.",
    retentionDays: "Enter the retention in whole days.",
    caBundle: "Paste the CA bundle as PEM.",
    sftpHost: "Enter the server.",
    port: "Enter a port between 1 and 65535.",
    user: "Enter the user.",
    basePath: "Enter an absolute directory (starting with /).",
    passwordReentry:
      "The server, user or base path changed: enter the password again, or set a key. A stored password is never sent to a new endpoint.",
    password: "Enter the password.",
    privateKey: "Paste the private key (PEM or OpenSSH format).",
  },
  nl: {
    name: "Vul een naam in.",
    nameLength: "Maximaal 100 tekens.",
    diskPath: "Vul een map onder de schijfroot in.",
    diskRelative: "Gebruik een relatief pad zonder „..”.",
    s3Endpoint: "Vul een https://-URL in, of laat het leeg voor AWS S3.",
    region: "Vul de regio in.",
    bucket: "Vul de bucket in.",
    accessKeyId: "Vul de access key ID in.",
    secretAccessKey: "Vul de secret access key in.",
    retentionDays: "Vul de bewaartermijn in hele dagen in.",
    caBundle: "Plak de CA-bundel als PEM.",
    sftpHost: "Vul de server in.",
    port: "Vul een poort in tussen 1 en 65535.",
    user: "Vul de gebruiker in.",
    basePath: "Vul een absolute map in (beginnend met /).",
    passwordReentry:
      "De server, de gebruiker of het basispad is gewijzigd: voer het wachtwoord opnieuw in, of stel een sleutel in. Een opgeslagen wachtwoord wordt nooit naar een nieuw eindpunt gestuurd.",
    password: "Vul het wachtwoord in.",
    privateKey: "Plak de privésleutel (PEM- of OpenSSH-formaat).",
  },
});

/**
 * The destination form model: values, validation (incl. D-140: a stored
 * SFTP or WebDAV password is never sent to a new endpoint) and the mapping to the
 * create/update inputs. An update sends the whole settings input of the
 * type (it replaces the non-secret configuration); credentials only when
 * entered.
 */

export type Original = NonNullable<DestinationDetailQuery["destination"]>;

export type SftpAuthChoice = "keep" | "password" | "privateKey" | "later";

export interface DestinationFormValues {
  name: string;
  type: DestinationType;
  noDelete: boolean;
  disk: { path: string };
  s3: {
    endpoint: string;
    region: string;
    bucket: string;
    prefix: string;
    pathStyle: boolean;
    encryption: S3Encryption;
    kmsKeyId: string;
    storageClass: string;
    checksumMode: S3ChecksumMode;
    conditionalWrites: boolean;
    objectLock: boolean;
    objectLockMode: ObjectLockMode;
    objectLockRetentionDays: string;
    accessKeyId: string;
    secretAction: "keep" | "set";
    secretAccessKey: string;
    caBundlePem: string;
  };
  sftp: {
    host: string;
    port: string;
    user: string;
    basePath: string;
    dir: string;
    auth: SftpAuthChoice;
    password: string;
    privateKey: string;
    privateKeyPassphrase: string;
  };
  webdav: {
    url: string;
    flavor: WebdavFlavor;
    username: string;
    passwordAction: "keep" | "set";
    password: string;
    path: string;
    caBundlePem: string;
    timeoutSeconds: string;
  };
}

/** Server field names (VALIDATION extensions.field) → form fields. */
export const SERVER_FIELDS = {
  name: "name",
  type: "type",
  "disk.path": "disk.path",
  s3: "s3.bucket",
  "s3.endpoint": "s3.endpoint",
  "s3.region": "s3.region",
  "s3.bucket": "s3.bucket",
  "s3.prefix": "s3.prefix",
  "s3.kmsKeyId": "s3.kmsKeyId",
  "s3.storageClass": "s3.storageClass",
  "s3.objectLockMode": "s3.objectLockMode",
  objectLockMode: "s3.objectLockMode",
  "s3.objectLockRetentionDays": "s3.objectLockRetentionDays",
  "s3.accessKeyId": "s3.accessKeyId",
  "s3.secretAccessKey": "s3.secretAccessKey",
  "s3.caBundlePem": "s3.caBundlePem",
  sftp: "sftp.host",
  "sftp.host": "sftp.host",
  "sftp.port": "sftp.port",
  "sftp.user": "sftp.user",
  "sftp.basePath": "sftp.basePath",
  "sftp.dir": "sftp.dir",
  "sftp.password": "sftp.password",
  "sftp.privateKey": "sftp.privateKey",
  "sftp.privateKeyPassphrase": "sftp.privateKeyPassphrase",
  webdav: "webdav.url",
  "webdav.url": "webdav.url",
  "webdav.flavor": "webdav.flavor",
  "webdav.username": "webdav.username",
  "webdav.password": "webdav.password",
  "webdav.path": "webdav.path",
  "webdav.caBundlePem": "webdav.caBundlePem",
  "webdav.timeoutSeconds": "webdav.timeoutSeconds",
} as const;

export type FormField = (typeof SERVER_FIELDS)[keyof typeof SERVER_FIELDS];
export const FORM_FIELDS: readonly FormField[] = [...new Set(Object.values(SERVER_FIELDS))];

export function emptyValues(type: DestinationType = "S3"): DestinationFormValues {
  return {
    name: "",
    type,
    noDelete: false,
    disk: { path: "" },
    s3: {
      endpoint: "",
      region: "",
      bucket: "",
      prefix: "",
      pathStyle: false,
      encryption: "SSE_S3",
      kmsKeyId: "",
      storageClass: "",
      checksumMode: "WHEN_SUPPORTED",
      conditionalWrites: true,
      objectLock: false,
      objectLockMode: "GOVERNANCE",
      objectLockRetentionDays: "",
      accessKeyId: "",
      secretAction: "set",
      secretAccessKey: "",
      caBundlePem: "",
    },
    sftp: {
      host: "",
      port: "",
      user: "",
      basePath: "",
      dir: "",
      auth: "later",
      password: "",
      privateKey: "",
      privateKeyPassphrase: "",
    },
    webdav: {
      url: "",
      flavor: "GENERIC",
      username: "",
      passwordAction: "set",
      password: "",
      path: "",
      caBundlePem: "",
      timeoutSeconds: "",
    },
  };
}

export function valuesFrom(d: Original): DestinationFormValues {
  const v = emptyValues(d.type);
  v.name = d.name;
  v.noDelete = d.noDelete;
  const s = d.settings;
  if (s?.disk) v.disk.path = s.disk.path;
  if (s?.s3) {
    const x = s.s3;
    v.s3 = {
      ...v.s3,
      endpoint: x.endpoint ?? "",
      region: x.region,
      bucket: x.bucket,
      prefix: x.prefix,
      pathStyle: x.pathStyle,
      encryption: x.encryption,
      kmsKeyId: x.kmsKeyId ?? "",
      storageClass: x.storageClass ?? "",
      checksumMode: x.checksumMode,
      conditionalWrites: x.conditionalWrites,
      objectLock: d.objectLockMode != null,
      objectLockMode: d.objectLockMode ?? "GOVERNANCE",
      objectLockRetentionDays:
        d.objectLockRetentionDays == null ? "" : String(d.objectLockRetentionDays),
      accessKeyId: x.accessKeyId,
      secretAction: x.hasSecretAccessKey ? "keep" : "set",
      caBundlePem: x.caBundlePem ?? "",
    };
  }
  if (s?.sftp) {
    const x = s.sftp;
    v.sftp = {
      ...v.sftp,
      host: x.host,
      port: String(x.port),
      user: x.user,
      basePath: x.basePath,
      dir: x.dir,
      auth: x.authMethod === "NONE" ? "later" : "keep",
    };
  }
  if (s?.webdav) {
    const x = s.webdav;
    v.webdav = {
      ...v.webdav,
      url: x.url,
      flavor: x.flavor,
      username: x.username,
      passwordAction: x.hasPassword ? "keep" : "set",
      path: x.path,
      caBundlePem: x.caBundlePem ?? "",
      timeoutSeconds: x.timeoutSeconds == null ? "" : String(x.timeoutSeconds),
    };
  }
  return v;
}

/** The URL as the server stores it (trimmed, no trailing slash). */
function normalizedUrl(url: string): string {
  return url.trim().replace(/\/+$/, "");
}

/** True when a stored WebDAV password must be entered again (D-140). */
export function webdavNeedsPassword(v: DestinationFormValues, orig: Original | undefined): boolean {
  const x = orig?.settings?.webdav;
  if (!x?.hasPassword) return false;
  return (
    normalizedUrl(v.webdav.url) !== normalizedUrl(x.url) ||
    v.webdav.flavor !== x.flavor ||
    v.webdav.username.trim() !== x.username
  );
}

/** Whether the update sends a new WebDAV password. */
export function effectiveWebdavPasswordAction(
  v: DestinationFormValues,
  orig: Original | undefined,
): "keep" | "set" {
  return webdavNeedsPassword(v, orig) ? "set" : v.webdav.passwordAction;
}

/** True when a stored SFTP password must be entered again (D-140). */
export function sftpNeedsPassword(v: DestinationFormValues, orig: Original | undefined): boolean {
  const x = orig?.settings?.sftp;
  if (x?.authMethod !== "PASSWORD" || !x.hasPassword) return false;
  const port = v.sftp.port.trim() === "" ? 22 : Number(v.sftp.port);
  return (
    v.sftp.host.trim() !== x.host ||
    port !== x.port ||
    v.sftp.user.trim() !== x.user ||
    v.sftp.basePath.trim() !== x.basePath
  );
}

export function effectiveSftpAuth(
  v: DestinationFormValues,
  orig: Original | undefined,
): SftpAuthChoice {
  return v.sftp.auth === "keep" && sftpNeedsPassword(v, orig) ? "password" : v.sftp.auth;
}

const PORT_RE = /^\d{1,5}$/;

export function buildSchema(orig: Original | undefined) {
  const str = z.string();
  return z
    .object({
      name: str.trim().min(1, E.name).max(100, E.nameLength),
      type: z.enum(["DISK", "S3", "SFTP", "WEBDAV"]),
      noDelete: z.boolean(),
      disk: z.object({ path: str.trim() }),
      s3: z.object({
        endpoint: str.trim(),
        region: str.trim(),
        bucket: str.trim(),
        prefix: str.trim(),
        pathStyle: z.boolean(),
        encryption: z.enum(["NONE", "SSE_S3", "SSE_KMS"]),
        kmsKeyId: str.trim(),
        storageClass: str.trim(),
        checksumMode: z.enum(["WHEN_SUPPORTED", "WHEN_REQUIRED"]),
        conditionalWrites: z.boolean(),
        objectLock: z.boolean(),
        objectLockMode: z.enum(["GOVERNANCE", "COMPLIANCE"]),
        objectLockRetentionDays: str.trim(),
        accessKeyId: str.trim(),
        secretAction: z.enum(["keep", "set"]),
        secretAccessKey: str,
        caBundlePem: str,
      }),
      sftp: z.object({
        host: str.trim(),
        port: str.trim(),
        user: str.trim(),
        basePath: str.trim(),
        dir: str.trim(),
        auth: z.enum(["keep", "password", "privateKey", "later"]),
        password: str,
        privateKey: str,
        privateKeyPassphrase: str,
      }),
      webdav: z.object({
        url: str.trim(),
        flavor: z.enum(["GENERIC", "NEXTCLOUD"]),
        username: str.trim(),
        passwordAction: z.enum(["keep", "set"]),
        password: str,
        path: str.trim(),
        caBundlePem: str,
        timeoutSeconds: str.trim(),
      }),
    })
    .superRefine((v, ctx) => {
      const issue = (path: string[], message: string) => {
        ctx.addIssue({ code: "custom", path, message });
      };
      const values: DestinationFormValues = v;
      switch (v.type) {
        case "DISK":
          if (v.disk.path === "") issue(["disk", "path"], E.diskPath);
          else if (v.disk.path.startsWith("/") || v.disk.path.split("/").includes(".."))
            issue(["disk", "path"], E.diskRelative);
          break;
        case "S3":
          if (v.s3.endpoint !== "" && !/^https?:\/\//i.test(v.s3.endpoint))
            issue(["s3", "endpoint"], E.s3Endpoint);
          if (v.s3.region === "") issue(["s3", "region"], E.region);
          if (v.s3.bucket === "") issue(["s3", "bucket"], E.bucket);
          if (v.s3.accessKeyId === "") issue(["s3", "accessKeyId"], E.accessKeyId);
          if (v.s3.secretAction === "set" && v.s3.secretAccessKey === "")
            issue(["s3", "secretAccessKey"], E.secretAccessKey);
          if (v.s3.objectLock) {
            const n = Number(v.s3.objectLockRetentionDays);
            if (!Number.isInteger(n) || n < 1)
              issue(["s3", "objectLockRetentionDays"], E.retentionDays);
          }
          if (v.s3.caBundlePem.trim() !== "" && !v.s3.caBundlePem.includes("BEGIN CERTIFICATE"))
            issue(["s3", "caBundlePem"], E.caBundle);
          break;
        case "SFTP": {
          if (v.sftp.host === "") issue(["sftp", "host"], E.sftpHost);
          if (
            v.sftp.port !== "" &&
            (!PORT_RE.test(v.sftp.port) || Number(v.sftp.port) < 1 || Number(v.sftp.port) > 65535)
          )
            issue(["sftp", "port"], E.port);
          if (v.sftp.user === "") issue(["sftp", "user"], E.user);
          if (!v.sftp.basePath.startsWith("/")) issue(["sftp", "basePath"], E.basePath);
          const auth = effectiveSftpAuth(values, orig);
          if (auth === "password" && v.sftp.password === "")
            issue(
              ["sftp", "password"],
              sftpNeedsPassword(values, orig) ? E.passwordReentry : E.password,
            );
          if (auth === "privateKey" && !v.sftp.privateKey.includes("PRIVATE KEY"))
            issue(["sftp", "privateKey"], E.privateKey);
          break;
        }
        case "WEBDAV": {
          const w = v.webdav;
          if (w.url === "") issue(["webdav", "url"], WT.errors.url);
          else if (!/^https?:\/\/[^/\s]/i.test(w.url))
            issue(["webdav", "url"], WT.errors.urlScheme);
          if (w.username === "") issue(["webdav", "username"], WT.errors.username);
          else if (w.username.includes(":")) issue(["webdav", "username"], WT.errors.usernameColon);
          if (webdavNeedsPassword(values, orig) && w.password === "")
            issue(["webdav", "password"], WT.errors.reentry);
          if (w.path.split("/").includes("..")) issue(["webdav", "path"], WT.errors.path);
          if (w.caBundlePem.trim() !== "" && !w.caBundlePem.includes("BEGIN CERTIFICATE"))
            issue(["webdav", "caBundlePem"], WT.errors.caBundle);
          if (w.timeoutSeconds !== "") {
            const n = Number(w.timeoutSeconds);
            if (!/^\d+$/.test(w.timeoutSeconds) || n < 5 || n > 600)
              issue(["webdav", "timeoutSeconds"], WT.errors.timeout);
          }
          break;
        }
      }
    });
}

function opt<K extends string, V>(key: K, value: V | "" | undefined): Partial<Record<K, V>> {
  return value === "" || value === undefined ? {} : ({ [key]: value } as Record<K, V>);
}

function s3Input(v: DestinationFormValues): S3DestinationInput {
  const x = v.s3;
  return {
    ...opt("endpoint", x.endpoint),
    region: x.region,
    bucket: x.bucket,
    prefix: x.prefix,
    pathStyle: x.pathStyle,
    encryption: x.encryption,
    ...(x.encryption === "SSE_KMS" ? opt("kmsKeyId", x.kmsKeyId) : {}),
    ...opt("storageClass", x.storageClass),
    checksumMode: x.checksumMode,
    conditionalWrites: x.conditionalWrites,
    ...(x.objectLock
      ? {
          objectLockMode: x.objectLockMode,
          objectLockRetentionDays: Number(x.objectLockRetentionDays),
        }
      : {}),
    accessKeyId: x.accessKeyId,
    ...(x.secretAction === "set" ? { secretAccessKey: x.secretAccessKey } : {}),
    ...opt("caBundlePem", x.caBundlePem.trim()),
  };
}

function sftpInput(v: DestinationFormValues, orig: Original | undefined): SftpDestinationInput {
  const x = v.sftp;
  const auth = effectiveSftpAuth(v, orig);
  return {
    host: x.host,
    ...(x.port !== "" ? { port: Number(x.port) } : {}),
    user: x.user,
    basePath: x.basePath,
    dir: x.dir,
    ...(auth === "password" ? { password: x.password } : {}),
    ...(auth === "privateKey"
      ? { privateKey: x.privateKey, ...opt("privateKeyPassphrase", x.privateKeyPassphrase) }
      : {}),
  };
}

function webdavInput(v: DestinationFormValues, orig: Original | undefined): WebdavDestinationInput {
  const x = v.webdav;
  const action = effectiveWebdavPasswordAction(v, orig);
  return {
    url: x.url,
    flavor: x.flavor,
    username: x.username,
    // An empty field never replaces a stored password; on create it means "none".
    ...(action === "set" && x.password !== "" ? { password: x.password } : {}),
    path: x.path,
    ...opt("caBundlePem", x.caBundlePem.trim()),
    ...(x.timeoutSeconds !== "" ? { timeoutSeconds: Number(x.timeoutSeconds) } : {}),
  };
}

function settingsInput(v: DestinationFormValues, orig: Original | undefined) {
  switch (v.type) {
    case "DISK":
      return { disk: { path: v.disk.path } };
    case "S3":
      return { s3: s3Input(v) };
    case "SFTP":
      return { sftp: sftpInput(v, orig) };
    case "WEBDAV":
      return { webdav: webdavInput(v, orig) };
  }
}

export function toCreateInput(v: DestinationFormValues): CreateDestinationInput {
  return {
    name: v.name.trim(),
    type: v.type,
    noDelete: v.noDelete,
    ...settingsInput(v, undefined),
  };
}

export function toUpdateInput(v: DestinationFormValues, orig: Original): UpdateDestinationInput {
  return {
    ...(v.name.trim() !== orig.name ? { name: v.name.trim() } : {}),
    ...(v.noDelete !== orig.noDelete ? { noDelete: v.noDelete } : {}),
    ...settingsInput(v, orig),
  };
}
