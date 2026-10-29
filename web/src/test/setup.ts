import "@testing-library/jest-dom/vitest";
import "@/lib/zod-config";

import { cleanup, configure } from "@testing-library/react";
import { toast } from "sonner";
import { afterEach, beforeEach } from "vitest";

import { mockMatchMedia } from "./match-media";

// findBy* and waitFor poll for up to 1 s by default, which full-app page
// tests exceed when the machine is busy (cold module transforms, parallel
// workers). Poll longer; passing tests still finish as soon as they match.
configure({ asyncUtilTimeout: 10_000 });

beforeEach(() => {
  mockMatchMedia(false);
});

afterEach(() => {
  cleanup();
  // sonner keeps toasts in module state; never let them leak into the next test.
  toast.dismiss();
  window.localStorage.clear();
  document.documentElement.classList.remove("dark");
});
