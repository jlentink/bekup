import { z } from "zod";

import type { SmtpMode, UpdateSmtpSettingsInput } from "@/graphql/gen/graphql";

export const SMTP_MODES: readonly SmtpMode[] = ["STARTTLS", "IMPLICIT_TLS", "AUTO", "NONE"];

/** AUTO and NONE may send mail and the AUTH credentials in clear text (D-180). */
export function mayBeUnencrypted(mode: SmtpMode): boolean {
  return mode === "AUTO" || mode === "NONE";
}

export const modeInfo: Record<SmtpMode, { label: string; port: number; lead: string }> = {
  STARTTLS: {
    label: "STARTTLS",
    port: 587,
    lead: "Plain connection upgraded with STARTTLS; refused when the server does not offer it.",
  },
  IMPLICIT_TLS: {
    label: "Implicit TLS",
    port: 465,
    lead: "TLS from the first byte (SMTPS).",
  },
  AUTO: {
    label: "Auto",
    port: 587,
    lead: "STARTTLS when offered, otherwise unencrypted (AUTH included). An attacker on the path can suppress the offer.",
  },
  NONE: {
    label: "None",
    port: 25,
    lead: "Never encrypt: mail and AUTH travel in clear text.",
  },
};

export type PasswordAction = "keep" | "set" | "clear";

const int = (min: number, max: number, msg: string) =>
  z
    .string()
    .trim()
    .regex(/^\d{1,5}$/, msg)
    .refine((v) => Number(v) >= min && Number(v) <= max, msg);

export const smtpSchema = z.object({
  enabled: z.boolean(),
  host: z.string().trim().min(1, "Enter the SMTP host.").max(253, "Host name is too long."),
  port: int(1, 65535, "Enter a port from 1 to 65535."),
  mode: z.enum(["AUTO", "STARTTLS", "IMPLICIT_TLS", "NONE"]),
  username: z.string().trim().max(256),
  passwordAction: z.enum(["keep", "set", "clear"]),
  password: z.string().max(1024),
  fromAddress: z.string().trim().pipe(z.email("Enter a valid sender address.")),
  fromName: z.string().trim().max(100),
  caBundlePem: z.string().max(65536),
  connectTimeoutSeconds: int(1, 60, "1 to 60 seconds."),
  sendTimeoutSeconds: int(5, 300, "5 to 300 seconds."),
});
export type SmtpFormValues = z.infer<typeof smtpSchema>;

export interface SmtpShape {
  configured: boolean;
  enabled: boolean;
  host: string;
  port: number;
  mode: SmtpMode;
  username?: string | null;
  hasPassword: boolean;
  fromAddress: string;
  fromName?: string | null;
  caBundlePem?: string | null;
  connectTimeoutSeconds: number;
  sendTimeoutSeconds: number;
}

export function toFormValues(s: SmtpShape): SmtpFormValues {
  return {
    enabled: s.enabled,
    host: s.host,
    port: String(s.port),
    mode: s.mode,
    username: s.username ?? "",
    passwordAction: s.hasPassword ? "keep" : "set",
    password: "",
    fromAddress: s.fromAddress,
    fromName: s.fromName ?? "",
    caBundlePem: s.caBundlePem ?? "",
    connectTimeoutSeconds: String(s.connectTimeoutSeconds),
    sendTimeoutSeconds: String(s.sendTimeoutSeconds),
  };
}

/**
 * True when a stored password cannot be kept: host, port or username
 * changed, and the server never sends a stored password to a new
 * endpoint.
 */
export function needsPasswordReentry(
  s: Pick<SmtpShape, "hasPassword" | "host" | "port" | "username">,
  v: Pick<SmtpFormValues, "host" | "port" | "username">,
): boolean {
  if (!s.hasPassword) return false;
  return (
    v.host.trim().toLowerCase() !== s.host.toLowerCase() ||
    v.port.trim() !== String(s.port) ||
    v.username.trim() !== (s.username ?? "")
  );
}

export function toInput(v: SmtpFormValues): UpdateSmtpSettingsInput {
  const input: UpdateSmtpSettingsInput = {
    enabled: v.enabled,
    host: v.host.trim(),
    port: Number(v.port),
    mode: v.mode,
    username: v.username.trim() === "" ? null : v.username.trim(),
    fromAddress: v.fromAddress.trim(),
    fromName: v.fromName.trim() === "" ? null : v.fromName.trim(),
    caBundlePem: v.caBundlePem.trim() === "" ? null : v.caBundlePem.trim(),
    connectTimeoutSeconds: Number(v.connectTimeoutSeconds),
    sendTimeoutSeconds: Number(v.sendTimeoutSeconds),
  };
  if (v.passwordAction === "clear") input.clearPassword = true;
  else if (v.passwordAction === "set" && v.password !== "") input.password = v.password;
  return input;
}
