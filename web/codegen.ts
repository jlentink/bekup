import { existsSync, readdirSync } from "node:fs";

import type { CodegenConfig } from "@graphql-codegen/cli";

/**
 * Client types come from the Go server's schema (gqlgen, task 1.2). Until that
 * exists, fall back to graphql/placeholder.graphqls so `pnpm gen` and the
 * typed `graphql()` helper keep working.
 */
const SCHEMA_DIR = "../internal/graph/schema";
const hasServerSchema =
  existsSync(SCHEMA_DIR) && readdirSync(SCHEMA_DIR).some((f) => f.endsWith(".graphqls"));

if (!hasServerSchema) {
  console.warn(
    `[gen] ${SCHEMA_DIR}/*.graphqls not found; using graphql/placeholder.graphqls (until task 1.2).`,
  );
}

const config: CodegenConfig = {
  schema: hasServerSchema ? `${SCHEMA_DIR}/*.graphqls` : "graphql/placeholder.graphqls",
  documents: ["src/**/*.{ts,tsx}", "!src/graphql/gen/**"],
  ignoreNoDocuments: true,
  hooks: {
    // Generated files are not type-checked against our strict tsconfig
    // (e.g. noUnusedLocals while there are no operations yet); their
    // exported types are still fully typed for consumers.
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
      },
      presetConfig: {
        fragmentMasking: { unmaskFunctionName: "getFragmentData" },
      },
    },
  },
};

export default config;
