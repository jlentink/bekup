import { describe, expect, it } from "vitest";

import { contrastRatio, mixOklch, parseOklch } from "./color";

describe("color math", () => {
  it("computes the WCAG extremes", () => {
    expect(contrastRatio(parseOklch("oklch(1 0 0)"), parseOklch("oklch(0 0 0)"))).toBeCloseTo(
      21,
      1,
    );
    expect(contrastRatio(parseOklch("oklch(0.5 0 0)"), parseOklch("oklch(0.5 0 0)"))).toBeCloseTo(
      1,
      5,
    );
  });

  it("matches a known sRGB value (#767676 on white is ~4.54:1)", () => {
    // #767676 ≈ oklch(0.5693 0 0)
    const ratio = contrastRatio(parseOklch("oklch(0.5693 0 0)"), parseOklch("oklch(1 0 0)"));
    expect(ratio).toBeGreaterThan(4.45);
    expect(ratio).toBeLessThan(4.65);
  });

  it("flags a low-contrast pair the palette gate must reject", () => {
    const ratio = contrastRatio(
      parseOklch("oklch(0.7 0.016 262)"),
      parseOklch("oklch(0.978 0.005 85)"),
    );
    expect(ratio).toBeLessThan(4.5);
  });

  it("mixes along the shorter hue arc", () => {
    const mixed = mixOklch(parseOklch("oklch(0.5 0.1 350)"), parseOklch("oklch(0.5 0.1 10)"), 0.5);
    expect(mixed.h === 0 || mixed.h === 360).toBe(true);
  });

  it("rejects non-oklch input", () => {
    expect(() => parseOklch("#fff")).toThrow();
  });
});
