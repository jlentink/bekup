import { messages } from "@/lib/messages";

const M = messages.sessions;

/** "JBSWY3DPEHPK3PXP" → "JBSW Y3DP EHPK 3PXP" for reading aloud and typing. */
export function groupKey(key: string): string {
  return key.replace(/\s+/g, "").replace(/(.{4})(?=.)/g, "$1 ");
}

/** Plain-text file content of a recovery code set. */
export function recoveryCodesText(codes: readonly string[], account: string, host: string): string {
  return [
    `bekup recovery codes for ${account} at ${host}`,
    "Each code works once. Keep them somewhere safe.",
    "",
    ...codes,
    "",
  ].join("\n");
}

/** Ordered: Edge and Chrome UAs also name Chrome and Safari. */
const BROWSERS: readonly [string, string][] = [
  ["Edg/", "Edge"],
  ["Firefox/", "Firefox"],
  ["Chrome/", "Chrome"],
  ["Safari/", "Safari"],
  ["curl/", "curl"],
];
const SYSTEMS: readonly [string, string][] = [
  ["Windows", "Windows"],
  ["iPhone", "iOS"],
  ["iPad", "iOS"],
  ["Android", "Android"],
  ["Mac OS X", "macOS"],
  ["Linux", "Linux"],
];

/** A short, readable browser + OS label from a User-Agent (display only). */
export function describeUserAgent(ua: string | null | undefined): string {
  if (!ua) return M.unknownDevice;
  const browser = BROWSERS.find(([marker]) => ua.includes(marker))?.[1];
  const os = SYSTEMS.find(([marker]) => ua.includes(marker))?.[1];
  if (browser && os) return `${browser} on ${os}`;
  return browser ?? os ?? M.unknownDevice;
}
