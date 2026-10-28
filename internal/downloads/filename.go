package downloads

import (
	"strings"
	"time"

	"github.com/jlentink/bekup/internal/artifactverify"
)

// Limits of the sanitized file name parts.
const (
	maxFileName  = 200
	maxNamePart  = 64
	fallbackName = "download"
)

// windowsReserved are device names that Windows refuses as a file's base
// name, whatever the extension.
var windowsReserved = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true, "com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true, "lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// SanitizeFileName returns a file name safe for Content-Disposition and
// every common file system: only ASCII letters, digits, '.', '-' and '_'
// (any other byte or rune, including path separators, control characters,
// quotes and non-ASCII, becomes '_', runs collapsed), no leading '.', '-'
// or '_' (hidden files, option-like names), no "..", no trailing '.', at most 200
// bytes, never a Windows device name and never empty.
func SanitizeFileName(name string) string {
	var b strings.Builder
	b.Grow(min(len(name), maxFileName))
	lastSub, lastDot := false, false
	for _, r := range name {
		if b.Len() >= 2*maxFileName {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
			lastSub, lastDot = false, false
		case r == '.':
			// No "..": a dot after a dot is dropped.
			if !lastDot {
				b.WriteByte('.')
			}
			lastSub, lastDot = false, true
		default:
			// '_' itself and every other rune.
			if !lastSub {
				b.WriteByte('_')
			}
			lastSub, lastDot = true, false
		}
	}
	s := strings.TrimLeft(b.String(), "._-")
	if len(s) > maxFileName {
		s = s[:maxFileName]
	}
	s = strings.TrimRight(s, ".")
	if s == "" || strings.Trim(s, "_") == "" {
		return fallbackName
	}
	base, _, _ := strings.Cut(s, ".")
	if windowsReserved[strings.ToLower(base)] {
		s = "_" + s
	}
	return s
}

// namePart sanitizes one component of a generated name ('_' separates the
// components, so it never appears inside one) and bounds its length.
func namePart(s string) string {
	p := SanitizeFileName(s)
	if p == fallbackName && s != fallbackName {
		return "x"
	}
	p = strings.ReplaceAll(p, ".", "-")
	p = strings.ReplaceAll(p, "_", "-")
	p = strings.Trim(p, "-")
	if len(p) > maxNamePart {
		p = strings.TrimRight(p[:maxNamePart], "-")
	}
	if p == "" {
		return "x"
	}
	return p
}

// ContentDisposition returns an attachment header value for name. The
// name is sanitized first, so the quoted form and the RFC 5987 form carry
// the same ASCII name and need no escaping.
func ContentDisposition(name string) string {
	s := SanitizeFileName(name)
	return `attachment; filename="` + s + `"; filename*=UTF-8''` + s
}

// storedFormat parses the file name of a stored object into the dump and
// stored extensions (artifactverify.StoredFormat).
func storedFormat(objectKey string, encrypted bool) (plainExt, storedExt string, err error) {
	return artifactverify.StoredFormat(objectKey, encrypted)
}

// downloadName builds "<job-slug>_<db>_<YYYYMMDDTHHMMSSZ>.<ext>".
func downloadName(jobSlug, db string, ts time.Time, ext string) string {
	return SanitizeFileName(namePart(jobSlug) + "_" + namePart(db) + "_" + ts.UTC().Format("20060102T150405Z") + "." + ext)
}
