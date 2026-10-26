import type { Plugin } from "vite";

/**
 * Removes runtime <style> injection from third-party libraries so the app runs
 * under `style-src 'self'` without 'unsafe-inline' or a nonce.
 *
 * Each rule targets one known module. If a targeted module is part of the
 * build but the pattern no longer matches (the library changed), the build
 * FAILS: a dependency update must never silently reintroduce a CSP violation.
 * The equivalent CSS lives in src/styles/csp-compat.css.
 *
 * react-style-singleton (used by react-remove-scroll) is not patched here; it
 * is aliased to src/lib/csp/style-singleton.ts in vite.config.ts.
 */
interface Rule {
  name: string;
  module: RegExp;
  pattern: RegExp;
  replacement: string;
}

const RULES: readonly Rule[] = [
  {
    // sonner calls __insertCSS("...") at import time, which appends a <style>.
    name: "sonner __insertCSS",
    module: /[\\/]node_modules[\\/]sonner[\\/]dist[\\/]index\.m?js$/,
    pattern: /__insertCSS\("(?:[^"\\]|\\.)*"\);?/g,
    replacement: "/* bekup: sonner CSS is imported from sonner/dist/styles.css */",
  },
  {
    // Radix Select renders <style dangerouslySetInnerHTML> in its viewport.
    name: "radix select viewport <style>",
    module: /[\\/]node_modules[\\/]@radix-ui[\\/]react-select[\\/]dist[\\/]index\.m?js$/,
    pattern:
      /jsx\(\s*"style",\s*\{\s*dangerouslySetInnerHTML:\s*\{\s*__html:\s*`[^`]*`\s*\},\s*nonce\s*\}\s*\)/g,
    replacement: "null",
  },
  {
    // Radix ScrollArea renders <style dangerouslySetInnerHTML> in its viewport.
    name: "radix scroll-area viewport <style>",
    module: /[\\/]node_modules[\\/]@radix-ui[\\/]react-scroll-area[\\/]dist[\\/]index\.m?js$/,
    pattern:
      /jsx\(\s*"style",\s*\{\s*dangerouslySetInnerHTML:\s*\{\s*__html:\s*`[^`]*`\s*\},\s*nonce\s*\}\s*\)/g,
    replacement: "null",
  },
];

export function cspStrictStyles(): Plugin {
  return {
    name: "bekup:csp-strict-styles",
    enforce: "pre",
    transform: {
      order: "pre",
      handler(code, id) {
        const path = id.split("?")[0] ?? id;
        const rule = RULES.find((r) => r.module.test(path));
        if (!rule) return null;
        let count = 0;
        const out = code.replace(rule.pattern, () => {
          count++;
          return rule.replacement;
        });
        if (count === 0) {
          this.error(
            `[csp-strict-styles] rule "${rule.name}" no longer matches ${path}. ` +
              "The library changed how it injects styles; update the rule and " +
              "src/styles/csp-compat.css, then run `pnpm csp-check`.",
          );
        }
        return { code: out, map: null };
      },
    },
  };
}
