import { CombinedGraphQLErrors, ServerError } from "@apollo/client/errors";
import type { FieldValues, Path, UseFormSetError } from "react-hook-form";

import { messages } from "./messages";

/**
 * User-facing error codes of the GraphQL API (internal/graph/apierror,
 * D-105) plus the codes the server itself produces. Never rename one.
 */
export const ApiCode = {
  unauthenticated: "UNAUTHENTICATED",
  forbidden: "FORBIDDEN",
  stepUpRequired: "STEP_UP_REQUIRED",
  requires2FAEnrollment: "REQUIRES_2FA_ENROLLMENT",
  validation: "VALIDATION",
  notFound: "NOT_FOUND",
  rateLimited: "RATE_LIMITED",
  conflict: "CONFLICT",
  passwordChangeRequired: "PASSWORD_CHANGE_REQUIRED",
  secondFactorRequired: "SECOND_FACTOR_REQUIRED",
  internal: "INTERNAL",
} as const;

export type ApiCodeValue = (typeof ApiCode)[keyof typeof ApiCode];

/** One user-facing error, normalized from a GraphQL or transport error. */
export interface ApiErrorInfo {
  /** extensions.code; "NETWORK" for transport failures without a code. */
  code: string;
  /** The server's message (user-safe by contract) or a local fallback. */
  message: string;
  /** extensions.field of a VALIDATION error. */
  field?: string;
  /** extensions.requestId of a masked INTERNAL error. */
  requestId?: string;
}

function str(v: unknown): string | undefined {
  return typeof v === "string" && v !== "" ? v : undefined;
}

/** Normalizes any error Apollo hands out into a list of ApiErrorInfo. */
export function apiErrors(error: unknown): ApiErrorInfo[] {
  if (error == null) return [];
  if (CombinedGraphQLErrors.is(error)) {
    return error.errors.map((e) => {
      const ext = e.extensions ?? {};
      const info: ApiErrorInfo = { code: str(ext["code"]) ?? ApiCode.internal, message: e.message };
      const field = str(ext["field"]);
      const requestId = str(ext["requestId"]);
      if (field) info.field = field;
      if (requestId) info.requestId = requestId;
      return info;
    });
  }
  if (ServerError.is(error)) {
    // Refusals by the HTTP layer (rate limit, body limit, origin check).
    if (error.statusCode === 429) {
      return [{ code: ApiCode.rateLimited, message: messages.errors.rateLimited }];
    }
    if (error.statusCode === 401) {
      return [{ code: ApiCode.unauthenticated, message: messages.errors.sessionEnded }];
    }
    if (error.statusCode === 403) {
      return [{ code: ApiCode.forbidden, message: messages.errors.forbidden }];
    }
    return [{ code: "NETWORK", message: messages.errors.network }];
  }
  return [{ code: "NETWORK", message: messages.errors.network }];
}

/** The first error, if any. */
export function firstApiError(error: unknown): ApiErrorInfo | undefined {
  return apiErrors(error)[0];
}

/** True when any error carries code. */
export function hasApiCode(error: unknown, code: ApiCodeValue): boolean {
  return apiErrors(error).some((e) => e.code === code);
}

/** Capitalizes the server's lower-case sentences for display. */
function sentence(text: string): string {
  const t = text.trim();
  if (t === "") return t;
  const s = t.charAt(0).toUpperCase() + t.slice(1);
  return /[.!?]$/.test(s) ? s : `${s}.`;
}

/**
 * A message to show for error: the server's own message for codes whose
 * messages are specific and user-safe (VALIDATION, RATE_LIMITED, CONFLICT,
 * STEP_UP_REQUIRED, ...), fixed texts otherwise. INTERNAL errors carry
 * their request ID so the operator can find the log line.
 */
export function userMessage(error: unknown): string {
  const e = firstApiError(error);
  if (!e) return messages.errors.generic;
  switch (e.code) {
    case ApiCode.internal:
      return e.requestId
        ? `${messages.errors.internal} ${messages.common.requestId(e.requestId)}`
        : messages.errors.internal;
    case ApiCode.forbidden:
      return e.message ? sentence(e.message) : messages.errors.forbidden;
    case ApiCode.notFound:
      return messages.errors.notFound;
    case ApiCode.unauthenticated:
      return messages.errors.sessionEnded;
    case "NETWORK":
      return e.message;
    default:
      return sentence(e.message);
  }
}

/**
 * Puts VALIDATION errors on the matching form fields. fieldMap maps the
 * server's field names to form field names (identity by default; fields
 * missing from the form are ignored). Returns true when at least one error
 * was placed, so the caller only toasts the rest.
 */
export function applyFieldErrors<T extends FieldValues>(
  error: unknown,
  setError: UseFormSetError<T>,
  fields: readonly Path<T>[],
  fieldMap: Partial<Record<string, Path<T>>> = {},
): boolean {
  let placed = false;
  for (const e of apiErrors(error)) {
    if (e.code !== ApiCode.validation || !e.field) continue;
    const target = fieldMap[e.field] ?? (fields as readonly string[]).find((f) => f === e.field);
    if (!target) continue;
    setError(
      target as Path<T>,
      { type: "server", message: sentence(e.message) },
      { shouldFocus: !placed },
    );
    placed = true;
  }
  return placed;
}
