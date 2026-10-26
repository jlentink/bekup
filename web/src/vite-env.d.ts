/// <reference types="vite/client" />

declare const __BEKUP_VERSION__: string;

interface ImportMetaEnv {
  /** Include the /design living style guide in a production build (csp-check only). */
  readonly VITE_ENABLE_DESIGN?: string;
}
