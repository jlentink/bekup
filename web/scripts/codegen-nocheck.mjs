// Prepends `// @ts-nocheck` to files written by graphql-codegen (passed as
// arguments by the afterAllFileWrite hook in codegen.ts). Idempotent.
import { readFileSync, writeFileSync } from "node:fs";

const MARKER = "// @ts-nocheck";

for (const file of process.argv.slice(2)) {
  if (!file.endsWith(".ts")) continue;
  const src = readFileSync(file, "utf8");
  if (!src.startsWith(MARKER)) writeFileSync(file, `${MARKER}\n${src}`);
}
