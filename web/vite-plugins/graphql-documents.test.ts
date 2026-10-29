import { readFileSync } from "node:fs";
import { join } from "node:path";

import { describe, expect, it } from "vitest";

import { parseGenerated, rewriteCalls } from "./graphql-documents.ts";

const gqlTs = `import * as types from './graphql';
const documents: Documents = {
    "\\n  query Me {\\n    me { id }\\n  }\\n": types.MeDocument,
    "\\n  fragment F on User { id }\\n": types.FFragmentDoc,
};
export function graphql(source: string) {}
`;
const graphqlTs = [
  'export const FFragmentDoc = {"kind":"Document","definitions":[]} as unknown as DocumentNode<FFragment, unknown>;',
  'export const MeDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition"}]} as unknown as DocumentNode<MeQuery, MeQueryVariables>;',
].join("\n");

describe("graphql-documents plugin", () => {
  const gen = parseGenerated(gqlTs, graphqlTs);

  it("maps call sources to pre-compiled documents", () => {
    expect(gen.bySource.get("\n  query Me {\n    me { id }\n  }\n")).toBe("MeDocument");
    expect(JSON.parse(gen.documents.get("MeDocument") ?? "{}")).toMatchObject({ kind: "Document" });
    expect(gen.documents.has("FFragmentDoc")).toBe(true);
  });

  it("replaces each call with an import of its own document module", () => {
    const code = [
      'import { graphql } from "@/graphql/gen";',
      "export const A = graphql(`\n  query Me {\n    me { id }\n  }\n`);",
      "export const B = graphql(`\n  fragment F on User { id }\n`);",
    ].join("\n");
    const out = rewriteCalls(code, gen, "x.ts") ?? "";
    expect(out).toContain('import __bekupGql_MeDocument from "virtual:bekup-gql/MeDocument";');
    expect(out).toContain('import __bekupGql_FFragmentDoc from "virtual:bekup-gql/FFragmentDoc";');
    expect(out).toContain("export const A = __bekupGql_MeDocument;");
    expect(out).not.toContain("graphql(`");
  });

  it("leaves modules without calls alone", () => {
    expect(rewriteCalls("export const x = 1;", gen, "x.ts")).toBeNull();
  });

  it("fails the build for stale codegen or dynamic templates", () => {
    expect(() => rewriteCalls("graphql(`query Gone { a }`)", gen, "x.ts")).toThrow(/pnpm gen/);
    expect(() => rewriteCalls("graphql(`query ${x}`)", gen, "x.ts")).toThrow(/plain text/);
  });

  it("understands the real generated files", () => {
    const dir = join(import.meta.dirname, "..", "src", "graphql", "gen");
    const real = parseGenerated(
      readFileSync(join(dir, "gql.ts"), "utf8"),
      readFileSync(join(dir, "graphql.ts"), "utf8"),
    );
    expect(real.bySource.size).toBeGreaterThan(50);
    for (const name of real.bySource.values()) expect(real.documents.has(name), name).toBe(true);
  });
});
