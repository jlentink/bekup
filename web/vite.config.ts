/// <reference types="vitest/config" />
import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath, URL } from "node:url";

import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig, type Plugin } from "vite";

import { cspStrictStyles } from "./vite-plugins/csp-strict-styles.ts";
import { graphqlDocuments } from "./vite-plugins/graphql-documents.ts";

const src = fileURLToPath(new URL("./src", import.meta.url));
const webRoot = fileURLToPath(new URL(".", import.meta.url));
const guides = fileURLToPath(new URL("../docs/guides", import.meta.url));
const styleSingletonShim = fileURLToPath(
  new URL("./src/lib/csp/style-singleton.ts", import.meta.url),
);

/**
 * The release version from the repository's VERSION file (`../VERSION`; the
 * Dockerfile's web stage copies it to /src/VERSION for this, or writes the
 * VERSION build-arg there: devel images carry x.y.z-dev.N, D-239). A
 * production build refuses to run without a valid SemVer version, so a missing file can
 * never ship a UI that says "v0.0.0-dev"; the dev server and vitest fall
 * back to it.
 */
function readVersion(strict: boolean): string {
  let version: string;
  try {
    version = readFileSync(new URL("../VERSION", import.meta.url), "utf8").trim();
  } catch (err) {
    if (strict) throw new Error("vite build: cannot read ../VERSION", { cause: err });
    return "0.0.0-dev";
  }
  if (!/^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$/.test(version)) {
    if (strict) throw new Error(`vite build: ../VERSION is not SemVer: ${JSON.stringify(version)}`);
    return "0.0.0-dev";
  }
  return version;
}

/**
 * `emptyOutDir` deletes dist/.gitkeep, which keeps `//go:embed all:dist`
 * (web/embed.go) compiling before the first web build. Put it back.
 */
function keepGitkeep(): Plugin {
  let outDir = "dist";
  return {
    name: "bekup:keep-gitkeep",
    apply: "build",
    configResolved(config) {
      outDir = config.build.outDir;
    },
    closeBundle() {
      writeFileSync(join(outDir, ".gitkeep"), "");
    },
  };
}

// The Go server (`bekup serve`) listens on :8080 in development.
const backend = process.env.BEKUP_DEV_BACKEND ?? "http://localhost:8080";

export default defineConfig(({ command }) => ({
  plugins: [cspStrictStyles(), graphqlDocuments(), react(), tailwindcss(), keepGitkeep()],
  resolve: {
    alias: [
      { find: /^@\//, replacement: `${src}/` },
      // CSP: never inject <style> elements at runtime (see the shim).
      { find: /^react-style-singleton$/, replacement: styleSingletonShim },
    ],
  },
  define: {
    __BEKUP_VERSION__: JSON.stringify(readVersion(command === "build")),
  },
  server: {
    port: 5173,
    strictPort: true,
    // Tests only: the API access snippets are compared with the external
    // API guide (src/features/api-access/snippets.test.ts imports it with
    // ?raw). The dev server keeps Vite's default file access.
    ...(process.env.VITEST ? { fs: { allow: [webRoot, guides] } } : {}),
    proxy: {
      "/graphql": { target: backend, changeOrigin: false },
      "/api": { target: backend, changeOrigin: false },
    },
  },
  preview: {
    port: 4173,
    strictPort: true,
  },
  build: {
    outDir: process.env.BEKUP_WEB_OUT_DIR ?? "dist",
    emptyOutDir: true,
    target: "es2023",
    // Never inline assets as data: URIs; fonts and images are served from
    // 'self' so the CSP needs no data: for fonts.
    assetsInlineLimit: 0,
    sourcemap: false,
    // Vite would otherwise inject a modulepreload polyfill; all supported
    // browsers have native modulepreload.
    modulePreload: { polyfill: false },
    rolldownOptions: {
      output: {
        // Stable vendor chunks: better caching across app releases.
        codeSplitting: {
          groups: [
            {
              name: "react",
              test: /[\\/]node_modules[\\/](react|react-dom|scheduler|react-router)[\\/]/,
            },
            {
              name: "graphql",
              test: /[\\/]node_modules[\\/](@apollo|graphql|graphql-sse|rxjs|@wry|optimism|tslib)[\\/]/,
            },
            {
              name: "ui",
              test: /[\\/]node_modules[\\/](@radix-ui|radix-ui|@floating-ui|sonner|lucide-react|react-remove-scroll[\w-]*|aria-hidden|use-[\w-]+|detect-node-es|get-nonce)[\\/]/,
            },
            {
              name: "forms",
              test: /[\\/]node_modules[\\/](react-hook-form|@hookform|zod)[\\/]/,
            },
          ],
        },
      },
    },
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    css: true,
    include: ["src/**/*.test.{ts,tsx}", "vite-plugins/**/*.test.ts"],
    restoreMocks: true,
    // Stability under parallel load (e.g. `just test` running Go and web
    // tests at once): page tests render the whole app in jsdom, so give
    // them generous budgets and leave CPU for the rest of the machine.
    // findBy*/waitFor use the asyncUtilTimeout set in src/test/setup.ts.
    testTimeout: 30_000,
    hookTimeout: 30_000,
    maxWorkers: "50%",
  },
}));
