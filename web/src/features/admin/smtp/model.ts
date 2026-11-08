import { z } from "zod";

import type { SmtpMode, UpdateSmtpSettingsInput } from "@/graphql/gen/graphql";
import { defineMessages } from "@/lib/i18n";

const M = defineMessages({
  en: {
    starttlsLead:
      "Plain connection upgraded with STARTTLS; refused when the server does not offer it.",
    implicitLabel: "Implicit TLS",
    implicitLead: "TLS from the first byte (SMTPS).",
    autoLabel: "Auto",
    autoLead:
      "STARTTLS when offered, otherwise unencrypted (AUTH included). An attacker on the path can suppress the offer.",
    noneLabel: "None",
    noneLead: "Never encrypt: mail and AUTH travel in clear text.",
    hostMissing: "Enter the SMTP host.",
    hostLong: "Host name is too long.",
    port: "Enter a port from 1 to 65535.",
    from: "Enter a valid sender address.",
    connectTimeout: "1 to 60 seconds.",
    sendTimeout: "5 to 300 seconds.",
  },
  nl: {
    starttlsLead:
      "Onversleutelde verbinding die met STARTTLS wordt beveiligd; geweigerd als de server het niet aanbiedt.",
    implicitLabel: "Impliciete TLS",
    implicitLead: "TLS vanaf de eerste byte (SMTPS).",
    autoLabel: "Automatisch",
    autoLead:
      "STARTTLS als het wordt aangeboden, anders onversleuteld (AUTH inbegrepen). Een aanvaller op de route kan het aanbod onderdrukken.",
    noneLabel: "Geen",
    noneLead: "Nooit versleutelen: e-mail en AUTH gaan als leesbare tekst over het netwerk.",
    hostMissing: "Vul de SMTP-host in.",
    hostLong: "De hostnaam is te lang.",
    port: "Vul een poort van 1 tot en met 65535 in.",
    from: "Vul een geldig afzenderadres in.",
    connectTimeout: "1 tot 60 seconden.",
    sendTimeout: "5 tot 300 seconden.",
  },
});

export const SMTP_MODES: readonly SmtpMode[] = ["STARTTLS", "IMPLICIT_TLS", "AUTO", "NONE"];

/** AUTO and NONE may send mail and the AUTH credentials in clear text (D-180). */
export function mayBeUnencrypted(mode: SmtpMode): boolean {
  return mode === "AUTO" || mode === "NONE";
}

export const modeInfo: Record<SmtpMode, { label: string; port: number; lead: string }> = {
  STARTTLS: {
    label: "STARTTLS",
    port: 587,
    lead: M.starttlsLead,
  },
  IMPLICIT_TLS: {
    label: M.implicitLabel,
    port: 465,
    lead: M.implicitLead,
  },
  AUTO: {
    label: M.autoLabel,
    port: 587,
    lead: M.autoLead,
  },
  NONE: {
    label: M.noneLabel,
    port: 25,
    lead: M.noneLead,
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
  host: z.string().trim().min(1, M.hostMissing).max(253, M.hostLong),
  port: int(1, 65535, M.port),
  mode: z.enum(["AUTO", "STARTTLS", "IMPLICIT_TLS", "NONE"]),
  username: z.string().trim().max(256),
  passwordAction: z.enum(["keep", "set", "clear"]),
  password: z.string().max(1024),
  fromAddress: z.string().trim().pipe(z.email(M.from)),
  fromName: z.string().trim().max(100),
  caBundlePem: z.string().max(65536),
  connectTimeoutSeconds: int(1, 60, M.connectTimeout),
  sendTimeoutSeconds: int(5, 300, M.sendTimeout),
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
