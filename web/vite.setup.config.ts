/**
 * The browser setup's own small UI (D-217): built into dist-setup/ and
 * embedded by web/embed.go (SetupFS), served only by `bekup setup web`.
 */
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath, URL } from "node:url";

import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig, type Plugin } from "vite";

import { cspStrictStyles } from "./vite-plugins/csp-strict-styles.ts";

const src = fileURLToPath(new URL("./src", import.meta.url));
const styleSingletonShim = fileURLToPath(
  new URL("./src/lib/csp/style-singleton.ts", import.meta.url),
);
// BEKUP_WEB_OUT_DIR: the CSP check's own build (scripts/csp-check.mjs).
const outDir =
  process.env.BEKUP_WEB_OUT_DIR ?? fileURLToPath(new URL("./dist-setup", import.meta.url));

/** emptyOutDir deletes dist-setup/.gitkeep, which keeps go:embed compiling. */
function keepGitkeep(): Plugin {
  return {
    name: "bekup:keep-gitkeep-setup",
    apply: "build",
    closeBundle() {
      writeFileSync(join(outDir, ".gitkeep"), "");
    },
  };
}

export default defineConfig({
  root: fileURLToPath(new URL("./setup", import.meta.url)),
  publicDir: fileURLToPath(new URL("./public", import.meta.url)),
  plugins: [cspStrictStyles(), react(), tailwindcss(), keepGitkeep()],
  resolve: {
    alias: [
      { find: /^@\//, replacement: `${src}/` },
      { find: /^react-style-singleton$/, replacement: styleSingletonShim },
    ],
  },
  build: {
    outDir,
    emptyOutDir: true,
    target: "es2023",
    assetsInlineLimit: 0,
    sourcemap: false,
    modulePreload: { polyfill: false },
  },
});
