import { describe, expect, it } from "vitest";

import { parseQRCodeSvg } from "./qr-svg";

/** The exact shape internal/auth/qrsvg.go produces. */
function serverSvg(size: number, path: string): string {
  return (
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${size} ${size}" shape-rendering="crispEdges" role="img" aria-label="QR code for your authenticator app">` +
    `<rect width="${size}" height="${size}" fill="#ffffff"/><path fill="#000000" d="${path}"/></svg>`
  );
}

describe("parseQRCodeSvg", () => {
  it("accepts the server's document", () => {
    expect(parseQRCodeSvg(serverSvg(29, "M4 4h7v1h-7zM12 4h1v1h-1z"))).toEqual({
      size: 29,
      path: "M4 4h7v1h-7zM12 4h1v1h-1z",
    });
  });

  it.each([
    serverSvg(29, "M4 4h7v1h-7z").replace("<path", "<script>alert(1)</script><path"),
    serverSvg(29, "M4 4h7v1h-7z").replace('fill="#000000"', 'fill="url(#x)"'),
    serverSvg(29, "M4 4 L 9 9"),
    serverSvg(29, "M4 4h7v1h-7z").replace('viewBox="0 0 29 29"', 'viewBox="0 0 29 30"'),
    serverSvg(5, "M4 4h1v1h-1z"),
    '<svg onload="x()"></svg>',
    "",
  ])("rejects anything else (%#)", (svg) => {
    expect(parseQRCodeSvg(svg)).toBeNull();
  });
});
