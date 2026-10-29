import { readFileSync } from "node:fs";
import { join } from "node:path";

import type { Plugin } from "vite";

/**
 * Production document splitting for the graphql-codegen client preset.
 *
 * The preset's `graphql(\`...\`)` looks documents up in one map in
 * src/graphql/gen/gql.ts, which references every operation of the app, so
 * all of them shipped in one eager chunk. In builds, this plugin replaces
 * each `graphql(\`...\`)` call with an import of a small virtual module
 * that holds exactly that pre-compiled document (taken from the generated
 * graphql.ts). Each document then lives in the chunk of the lazy route
 * that uses it, and the lookup map is tree-shaken away. The same job as
 * the preset's babel/swc plugin, without a babel pipeline (Vite 8 uses
 * oxc), and per module rather than per generated file.
 *
 * A call whose document is not in the generated output fails the build:
 * stale codegen (`pnpm gen`) must never ship an empty document.
 */
const VIRTUAL = "virtual:bekup-gql/";
const RESOLVED = `\0${VIRTUAL}`;
const CALL = /\bgraphql\(\s*`([^`]*)`\s*\)/g;

interface Generated {
  /** Source text of a graphql() call → export name in graphql.ts. */
  bySource: Map<string, string>;
  /** Export name → document JSON. */
  documents: Map<string, string>;
}

/** Parses the generated gql.ts / graphql.ts (exported for tests). */
export function parseGenerated(gqlTs: string, graphqlTs: string): Generated {
  const bySource = new Map<string, string>();
  const entry = /^\s+("(?:[^"\\]|\\.)*"): types\.(\w+),$/gm;
  const mapStart = gqlTs.indexOf("const documents");
  const mapEnd = gqlTs.indexOf("\n};", mapStart);
  if (mapStart < 0 || mapEnd < 0) throw new Error("graphql-documents: no documents map in gql.ts");
  for (const m of gqlTs.slice(mapStart, mapEnd).matchAll(entry)) {
    bySource.set(JSON.parse(m[1] ?? '""') as string, m[2] ?? "");
  }
  const documents = new Map<string, string>();
  const doc = /^export const (\w+) = (\{.*\}) as unknown as DocumentNode<.*$/gm;
  for (const m of graphqlTs.matchAll(doc)) {
    const json = m[2] ?? "";
    JSON.parse(json); // must be plain JSON
    documents.set(m[1] ?? "", json);
  }
  return { bySource, documents };
}

/** Rewrites the graphql() calls of one module (exported for tests). */
export function rewriteCalls(code: string, gen: Generated, id: string): string | null {
  if (!code.includes("graphql(`")) return null;
  const used = new Set<string>();
  const out = code.replace(CALL, (_all, source: string) => {
    if (source.includes("${") || source.includes("\\")) {
      throw new Error(`graphql-documents: ${id}: graphql() templates must be plain text`);
    }
    const name = gen.bySource.get(source);
    if (!name || !gen.documents.has(name)) {
      throw new Error(`graphql-documents: ${id}: document not generated; run \`pnpm gen\``);
    }
    used.add(name);
    return `__bekupGql_${name}`;
  });
  if (used.size === 0) return null;
  const imports = [...used]
    .map((n) => `import __bekupGql_${n} from ${JSON.stringify(VIRTUAL + n)};`)
    .join("\n");
  return `${imports}\n${out}`;
}

export function graphqlDocuments(): Plugin {
  let gen: Generated | null = null;
  let genDir = "";
  return {
    name: "bekup:graphql-documents",
    apply: "build",
    enforce: "pre",
    configResolved(config) {
      genDir = join(config.root, "src", "graphql", "gen");
    },
    buildStart() {
      const gqlPath = join(genDir, "gql.ts");
      const graphqlPath = join(genDir, "graphql.ts");
      gen = parseGenerated(readFileSync(gqlPath, "utf8"), readFileSync(graphqlPath, "utf8"));
      this.addWatchFile(gqlPath);
      this.addWatchFile(graphqlPath);
    },
    resolveId(source) {
      return source.startsWith(VIRTUAL) ? `\0${source}` : null;
    },
    load(id) {
      if (!id.startsWith(RESOLVED) || !gen) return null;
      const json = gen.documents.get(id.slice(RESOLVED.length));
      if (json === undefined) throw new Error(`graphql-documents: unknown document ${id}`);
      return `export default /* @__PURE__ */ JSON.parse(${JSON.stringify(json)});`;
    },
    transform(code, id) {
      if (!gen || !/\.tsx?$/.test(id) || id.includes("/src/graphql/gen/")) return null;
      if (id.includes("/node_modules/")) return null;
      const out = rewriteCalls(code, gen, id);
      return out === null ? null : { code: out, map: null };
    },
  };
}
