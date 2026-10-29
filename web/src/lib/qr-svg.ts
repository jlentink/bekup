/**
 * The TOTP QR code arrives as an SVG document (internal/auth/qrsvg.go:
 * one white rect, one black path of "M x yhNv1h-Nz" runs). Inserting markup
 * is impossible under our CSP (Trusted Types 'none', no innerHTML, no
 * DOMParser, img-src without data:/blob:), so the document is parsed with
 * a strict grammar and re-rendered as React elements. Anything that does
 * not match exactly is rejected (the user can still type the key).
 */
export interface QRCodeShape {
  /** viewBox width = height, in modules (quiet zone included). */
  size: number;
  /** The path data of the dark modules. */
  path: string;
}

const SVG_RE =
  /^<svg xmlns="http:\/\/www\.w3\.org\/2000\/svg" viewBox="0 0 (\d{1,3}) (\d{1,3})"[^<>]*><rect width="\d{1,3}" height="\d{1,3}" fill="#ffffff"\/><path fill="#000000" d="([Mhvz0-9 -]*)"\/><\/svg>$/;

export function parseQRCodeSvg(svg: string): QRCodeShape | null {
  if (svg.length > 200_000) return null;
  const m = SVG_RE.exec(svg);
  if (!m) return null;
  const [, w, h, path] = m;
  if (w !== h || !path) return null;
  // Every subpath is exactly "M<x> <y>h<n>v1h-<n>z".
  if (!/^(M\d+ \d+h\d+v1h-\d+z)*$/.test(path)) return null;
  const size = Number(w);
  if (size < 21 || size > 200) return null;
  return { size, path };
}
