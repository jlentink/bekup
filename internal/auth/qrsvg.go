package auth

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/qr"
)

// qrQuietZone is the light border around a QR code, in modules (ISO/IEC
// 18004 requires 4).
const qrQuietZone = 4

// maxQRContent bounds what QRCodeSVG encodes (an otpauth URI is far
// shorter).
const maxQRContent = 1024

// QRCodeSVG renders content as a QR code (error correction level M) in a
// self-contained SVG document: one white background rectangle and one
// black path, no scripts, styles, external references or data: URIs, so it
// can be inlined under the strict CSP (D-082: img-src has no data:). Each
// dark module is a 1×1 square of the viewBox; the image scales freely.
func QRCodeSVG(content string) (string, error) {
	if content == "" || len(content) > maxQRContent {
		return "", fmt.Errorf("auth: QR content must be 1..%d bytes", maxQRContent)
	}
	code, err := qr.Encode(content, qr.M, qr.Auto)
	if err != nil {
		return "", fmt.Errorf("auth: encode QR code: %w", err)
	}
	b := code.Bounds()
	w, h := b.Dx(), b.Dy()
	size := strconv.Itoa(w + 2*qrQuietZone)
	var sb strings.Builder
	sb.Grow(64 * w)
	sb.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 `)
	sb.WriteString(size + " " + size)
	sb.WriteString(`" shape-rendering="crispEdges" role="img" aria-label="QR code for your authenticator app">`)
	sb.WriteString(`<rect width="` + size + `" height="` + size + `" fill="#ffffff"/>`)
	sb.WriteString(`<path fill="#000000" d="`)
	for y := range h {
		for x := 0; x < w; {
			if !dark(code, b.Min.X+x, b.Min.Y+y) {
				x++
				continue
			}
			// One horizontal run of dark modules per subpath.
			run := 1
			for x+run < w && dark(code, b.Min.X+x+run, b.Min.Y+y) {
				run++
			}
			fmt.Fprintf(&sb, "M%d %dh%dv1h-%dz", x+qrQuietZone, y+qrQuietZone, run, run)
			x += run
		}
	}
	sb.WriteString(`"/></svg>`)
	return sb.String(), nil
}

// dark reports whether the module at (x, y) is dark.
func dark(code barcode.Barcode, x, y int) bool {
	r, _, _, _ := code.At(x, y).RGBA()
	return r < 0x8000
}
