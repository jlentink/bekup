import { z } from "zod";

import type { Role, TwoFactorPolicyInput } from "@/graphql/gen/graphql";
import { defineMessages, plural } from "@/lib/i18n";

export const POLICY_ROLES: readonly Role[] = ["ADMIN", "MAINTAINER", "USER"];
export const MAX_GRACE_DAYS = 90;

export const policyMessages = defineMessages({
  en: {
    eyebrow: "Administration · Security",
    title: "Two-factor policy",
    lead: "Decide which roles must use a second factor (an authenticator app or a passkey) to keep using bekup.",
    back: "Users",
    rolesHeading: "Who must use 2FA",
    rolesLead:
      "Required roles get a grace period to enroll. Afterwards an account without a factor can only enroll one: every other page and API call is refused until it does.",
    requireForAll: "Require for every role",
    required: "2FA required",
    optional: "Optional",
    requireForAllHint: "Also covers roles added in later versions.",
    roleHint: {
      ADMIN: "Strongly recommended: admins manage users, keys and security settings.",
      MAINTAINER: "Maintainers change connections, destinations and every job.",
      USER: "Backup owners can restore and download their jobs' data.",
    } satisfies Record<Role, string>,
    graceHeading: "Grace period",
    graceLead:
      "Days a person keeps full access after the requirement starts for their role (or after their account was created, if later). 0 means immediately.",
    graceLabel: "Grace period in days",
    graceUnit: "days",
    graceInvalid: `Enter a whole number from 0 to ${String(MAX_GRACE_DAYS)}.`,
    effectsHeading: "What happens on save",
    effectNewlyRequired: (roles: string) =>
      `${roles}: the grace period starts now. People without a factor see a deadline banner, then must enroll.`,
    effectGrace: (days: number) =>
      days === 0
        ? "No grace period: affected accounts without a factor must enroll at their next request."
        : plural(days, {
            one: "Accounts without a factor keep full access for 1 day.",
            other: `Accounts without a factor keep full access for ${String(days)} days.`,
          }),
    effectNone: "No change in who must enroll.",
    passkeysUnavailable:
      "Passkeys are unavailable on this server (no HTTPS). People enroll an authenticator app instead, so the policy can still be met.",
    weakenTitle: "This weakens the policy",
    weakenLead: "Saving raises a security alert to every admin and is written to the audit log.",
    weakenRole: (role: string) => `${role} no longer requires a second factor.`,
    weakenGrace: (from: number, to: number) =>
      `The grace period grows from ${String(from)} to ${String(to)} days.`,
    weakenAll: "Not every role is required any more.",
    save: "Save policy",
    saving: "Saving…",
    saved: "Two-factor policy saved.",
    confirmWeaken: "Weaken the policy",
    noChanges: "Nothing changed.",
    lastChanged: (when: string) => `Last changed ${when}`,
    builtIn: "Built-in default",
  },
  nl: {
    eyebrow: "Beheer · Beveiliging",
    title: "Beleid voor tweestapsverificatie",
    lead: "Bepaal welke rollen een tweede factor (een authenticator-app of een passkey) moeten gebruiken om bekup te blijven gebruiken.",
    back: "Gebruikers",
    rolesHeading: "Wie moet 2FA gebruiken",
    rolesLead:
      "Verplichte rollen krijgen een overgangsperiode om een factor in te stellen. Daarna kan een account zonder factor alleen nog een factor instellen: elke andere pagina en API-aanroep wordt geweigerd tot dat gebeurd is.",
    requireForAll: "Verplicht voor elke rol",
    required: "2FA verplicht",
    optional: "Optioneel",
    requireForAllHint: "Geldt ook voor rollen die in latere versies worden toegevoegd.",
    roleHint: {
      ADMIN:
        "Sterk aanbevolen: beheerders beheren gebruikers, sleutels en beveiligingsinstellingen.",
      MAINTAINER: "Onderhouders wijzigen verbindingen, bestemmingen en elke taak.",
      USER: "Eigenaren van back-uptaken kunnen de gegevens van hun taken herstellen en downloaden.",
    },
    graceHeading: "Overgangsperiode",
    graceLead:
      "Het aantal dagen dat iemand volledige toegang houdt nadat de verplichting voor zijn rol ingaat (of nadat het account is aangemaakt, als dat later is). 0 betekent meteen.",
    graceLabel: "Overgangsperiode in dagen",
    graceUnit: "dagen",
    graceInvalid: `Vul een geheel getal van 0 tot en met ${String(MAX_GRACE_DAYS)} in.`,
    effectsHeading: "Wat er gebeurt bij opslaan",
    effectNewlyRequired: (roles: string) =>
      `${roles}: de overgangsperiode begint nu. Wie geen factor heeft, ziet een banner met de deadline en moet daarna een factor instellen.`,
    effectGrace: (days: number) =>
      days === 0
        ? "Geen overgangsperiode: betrokken accounts zonder factor moeten bij hun volgende verzoek een factor instellen."
        : plural(days, {
            one: "Accounts zonder factor houden 1 dag volledige toegang.",
            other: `Accounts zonder factor houden ${String(days)} dagen volledige toegang.`,
          }),
    effectNone: "Geen verandering in wie een factor moet instellen.",
    passkeysUnavailable:
      "Passkeys zijn niet beschikbaar op deze server (geen HTTPS). Gebruikers stellen in plaats daarvan een authenticator-app in, zodat aan het beleid kan worden voldaan.",
    weakenTitle: "Dit verzwakt het beleid",
    weakenLead: "Opslaan stuurt elke beheerder een beveiligingsmelding en komt in de auditlog.",
    weakenRole: (role: string) => `${role} heeft niet langer een tweede factor nodig.`,
    weakenGrace: (from: number, to: number) =>
      `De overgangsperiode wordt langer: van ${String(from)} naar ${String(to)} dagen.`,
    weakenAll: "Niet langer elke rol is verplicht.",
    save: "Beleid opslaan",
    saving: "Opslaan…",
    saved: "Beleid voor tweestapsverificatie opgeslagen.",
    confirmWeaken: "Beleid verzwakken",
    noChanges: "Er is niets gewijzigd.",
    lastChanged: (when: string) => `Laatst gewijzigd ${when}`,
    builtIn: "Ingebouwde standaard",
  },
});

export const policySchema = z.object({
  requireForAll: z.boolean(),
  requiredRoles: z.array(z.enum(["ADMIN", "MAINTAINER", "USER"])),
  gracePeriodDays: z
    .string()
    .trim()
    .regex(/^\d{1,3}$/, policyMessages.graceInvalid)
    .refine((v) => Number(v) <= MAX_GRACE_DAYS, policyMessages.graceInvalid),
});
export type PolicyFormValues = z.infer<typeof policySchema>;

export interface PolicyShape {
  requireForAll: boolean;
  requiredRoles: readonly Role[];
  gracePeriodDays: number;
}

export function toFormValues(p: PolicyShape): PolicyFormValues {
  return {
    requireForAll: p.requireForAll,
    requiredRoles: POLICY_ROLES.filter((r) => p.requiredRoles.includes(r)),
    gracePeriodDays: String(p.gracePeriodDays),
  };
}

export function toInput(v: PolicyFormValues): TwoFactorPolicyInput {
  return {
    requireForAll: v.requireForAll,
    requiredRoles: POLICY_ROLES.filter((r) => v.requiredRoles.includes(r)),
    gracePeriodDays: Number(v.gracePeriodDays),
  };
}

/** The roles that must use a second factor under p. */
export function effectiveRoles(p: Pick<PolicyShape, "requireForAll" | "requiredRoles">): Role[] {
  return p.requireForAll
    ? [...POLICY_ROLES]
    : POLICY_ROLES.filter((r) => p.requiredRoles.includes(r));
}

export type Weakening =
  { kind: "role"; role: Role } | { kind: "all" } | { kind: "grace"; from: number; to: number };

/**
 * How next is weaker than current: a role that stops being required,
 * dropping "every role", or a longer grace period. Empty when next is
 * at least as strict (the server raises an alert for the same cases).
 */
export function weakenings(current: PolicyShape, next: PolicyShape): Weakening[] {
  const out: Weakening[] = [];
  const now = new Set(effectiveRoles(current));
  const then = new Set(effectiveRoles(next));
  for (const r of POLICY_ROLES) if (now.has(r) && !then.has(r)) out.push({ kind: "role", role: r });
  if (current.requireForAll && !next.requireForAll) out.push({ kind: "all" });
  if (next.gracePeriodDays > current.gracePeriodDays) {
    out.push({ kind: "grace", from: current.gracePeriodDays, to: next.gracePeriodDays });
  }
  return out;
}

/** Roles that become required with next (their grace period starts on save). */
export function newlyRequired(current: PolicyShape, next: PolicyShape): Role[] {
  const now = new Set(effectiveRoles(current));
  return effectiveRoles(next).filter((r) => !now.has(r));
}

export function samePolicy(a: PolicyShape, b: PolicyShape): boolean {
  const ra = effectiveRoles({ requireForAll: false, requiredRoles: a.requiredRoles });
  const rb = effectiveRoles({ requireForAll: false, requiredRoles: b.requiredRoles });
  return (
    a.requireForAll === b.requireForAll &&
    a.gracePeriodDays === b.gracePeriodDays &&
    ra.join() === rb.join()
  );
}
