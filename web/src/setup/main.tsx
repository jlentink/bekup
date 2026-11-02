// Must be first: configures zod before any schema is created (CSP, no eval).
import "../lib/zod-config";

import "@fontsource-variable/instrument-sans";
import "@fontsource-variable/jetbrains-mono";
import "@fontsource/instrument-serif/400.css";
import "@fontsource/instrument-serif/400-italic.css";
import "./setup.css";

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { SetupApp } from "./app";

const root = document.getElementById("root");
if (!root) throw new Error("#root element missing from index.html");

createRoot(root).render(
  <StrictMode>
    <SetupApp />
  </StrictMode>,
);
