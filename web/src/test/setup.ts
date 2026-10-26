import "@testing-library/jest-dom/vitest";
import "@/lib/zod-config";

import { cleanup } from "@testing-library/react";
import { afterEach, beforeEach } from "vitest";

import { mockMatchMedia } from "./match-media";

beforeEach(() => {
  mockMatchMedia(false);
});

afterEach(() => {
  cleanup();
  window.localStorage.clear();
  document.documentElement.classList.remove("dark");
});
