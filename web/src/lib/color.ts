/** Minimal OKLCH → sRGB math for the design-token contrast tests. */

export interface Oklch {
  l: number;
  c: number;
  h: number;
}

const OKLCH_RE = /^oklch\(\s*([\d.]+)\s+([\d.]+)\s+([\d.]+)\s*(?:\/\s*[\d.]+\s*)?\)$/;

export function parseOklch(value: string): Oklch {
  const m = OKLCH_RE.exec(value.trim());
  if (!m?.[1] || !m[2] || !m[3]) throw new Error(`not an oklch() color: ${value}`);
  return { l: Number(m[1]), c: Number(m[2]), h: Number(m[3]) };
}

/** color-mix(in oklch, a p%, b) with shorter-hue interpolation. */
export function mixOklch(a: Oklch, b: Oklch, weightA: number): Oklch {
  let dh = a.h - b.h;
  if (dh > 180) dh -= 360;
  if (dh < -180) dh += 360;
  return {
    l: a.l * weightA + b.l * (1 - weightA),
    c: a.c * weightA + b.c * (1 - weightA),
    h: (b.h + dh * weightA + 360) % 360,
  };
}

/** Linear-light sRGB channels, gamut-clipped to [0, 1]. */
export function oklchToLinearSrgb({ l, c, h }: Oklch): [number, number, number] {
  const hr = (h * Math.PI) / 180;
  const a = c * Math.cos(hr);
  const b = c * Math.sin(hr);
  const l_ = (l + 0.3963377774 * a + 0.2158037573 * b) ** 3;
  const m_ = (l - 0.1055613458 * a - 0.0638541728 * b) ** 3;
  const s_ = (l - 0.0894841775 * a - 1.291485548 * b) ** 3;
  const clip = (x: number) => Math.min(1, Math.max(0, x));
  return [
    clip(4.0767416621 * l_ - 3.3077115913 * m_ + 0.2309699292 * s_),
    clip(-1.2684380046 * l_ + 2.6097574011 * m_ - 0.3413193965 * s_),
    clip(-0.0041960863 * l_ - 0.7034186147 * m_ + 1.707614701 * s_),
  ];
}

export function relativeLuminance(color: Oklch): number {
  const [r, g, b] = oklchToLinearSrgb(color);
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

/** WCAG 2.x contrast ratio. */
export function contrastRatio(a: Oklch, b: Oklch): number {
  const la = relativeLuminance(a);
  const lb = relativeLuminance(b);
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}
