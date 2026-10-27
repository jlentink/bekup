// @ts-check
import js from "@eslint/js";
import { defineConfig } from "eslint/config";
import jsxA11y from "eslint-plugin-jsx-a11y";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import globals from "globals";
import tseslint from "typescript-eslint";

export default defineConfig(
  {
    ignores: ["dist", "coverage", ".csp-check-dist", "src/graphql/gen", "public/theme-init.js"],
  },
  {
    files: ["**/*.{ts,tsx}"],
    extends: [
      js.configs.recommended,
      tseslint.configs.strictTypeChecked,
      tseslint.configs.stylisticTypeChecked,
      reactHooks.configs.flat["recommended-latest"],
      jsxA11y.flatConfigs.strict,
    ],
    languageOptions: {
      ecmaVersion: 2023,
      globals: globals.browser,
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
    plugins: { "react-refresh": reactRefresh },
    rules: {
      "react-refresh/only-export-components": ["warn", { allowConstantExport: true }],
      "@typescript-eslint/restrict-template-expressions": ["error", { allowNumber: true }],
      "@typescript-eslint/consistent-type-imports": ["error", { fixStyle: "inline-type-imports" }],
      // Security: no string-to-code or raw HTML sinks in our own code.
      "no-eval": "error",
      "no-implied-eval": "error",
      "no-new-func": "error",
      // Live events are Server-Sent Events only (D-124). graphql-ws is in
      // the lockfile only as a transitive dependency of graphql-codegen and
      // an optional Apollo peer; the app must never import it.
      "no-restricted-imports": [
        "error",
        {
          paths: [
            {
              name: "graphql-ws",
              message: "No WebSocket transport (D-124): use the SSE link in src/lib/apollo.ts.",
            },
            {
              name: "@apollo/client/link/subscriptions",
              message: "No WebSocket transport (D-124): use the SSE link in src/lib/apollo.ts.",
            },
            { name: "subscriptions-transport-ws", message: "No WebSocket transport (D-124)." },
          ],
        },
      ],
      "no-restricted-syntax": [
        "error",
        {
          selector: "JSXAttribute[name.name='dangerouslySetInnerHTML']",
          message: "Raw HTML is not allowed (XSS; CSP blocks inline styles/scripts anyway).",
        },
        {
          selector: "MemberExpression[property.name=/^(innerHTML|outerHTML)$/]",
          message: "Use React rendering or textContent instead of innerHTML/outerHTML.",
        },
      ],
    },
  },
  {
    // shadcn/ui primitives: generated, exported with their variant helpers.
    files: ["src/components/ui/**/*.tsx"],
    rules: {
      "react-refresh/only-export-components": "off",
    },
  },
  {
    // Providers and route tables export hooks/constants alongside components.
    files: ["src/lib/**/*.tsx", "src/app/**/*.tsx"],
    rules: {
      "react-refresh/only-export-components": "off",
    },
  },
  {
    files: ["**/*.test.{ts,tsx}", "src/test/**"],
    rules: {
      "@typescript-eslint/no-non-null-assertion": "off",
    },
  },
  {
    files: ["*.config.{js,ts}", "codegen.ts", "scripts/**/*.mjs", "vite-plugins/**/*.ts"],
    languageOptions: { globals: globals.node },
  },
  {
    files: ["**/*.{js,mjs}"],
    extends: [js.configs.recommended],
    languageOptions: { globals: { ...globals.node, ...globals.browser } },
  },
);
