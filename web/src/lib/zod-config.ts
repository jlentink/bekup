import { z } from "zod";

/**
 * Zod v4 compiles object parsers with `new Function()` unless told not to.
 * Our CSP has no 'unsafe-eval' and enforces Trusted Types, so eval is blocked
 * (and its feature probe would raise a CSP violation report). Must run before
 * any schema is created: imported first in main.tsx (and in the test setup).
 */
z.config({ jitless: true });
