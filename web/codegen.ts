import type { CodegenConfig } from "@graphql-codegen/cli";

/** Client types come from the Go server's schema (gqlgen, task 1.2). */
const config: CodegenConfig = {
  schema: "../internal/graph/schema/*.graphqls",
  documents: ["src/**/*.{ts,tsx}", "!src/graphql/gen/**"],
  ignoreNoDocuments: true,
  hooks: {
    // Generated files are not type-checked against our strict tsconfig
    // (e.g. noUnusedLocals); their exported types are still fully typed for
    // consumers.
    afterAllFileWrite: ["node scripts/codegen-nocheck.mjs"],
  },
  generates: {
    "src/graphql/gen/": {
      preset: "client",
      config: {
        useTypeImports: true,
        enumsAsTypes: true,
        // Unmapped custom scalars are `unknown`, never `any`.
        defaultScalarType: "unknown",
        // Server scalars (internal/graph/model/scalars.go) travel as strings.
        scalars: {
          Time: "string",
          UUID: "string",
          // JSON objects passed through (WebAuthn options/responses; model/json.go).
          JSON: "Record<string, unknown>",
        },
      },
      presetConfig: {
        fragmentMasking: { unmaskFunctionName: "getFragmentData" },
      },
    },
  },
};

export default config;
