package downloads

import (
	"errors"
	"strings"
	"time"
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

// errArtifactName reports a stored object name without a known format.
var errArtifactName = errors.New("downloads: unknown artifact file format")

// storedFormat parses the file name of a stored object
// ("<db>.dump.zst.age", "globals.globals.sql.zst", ...) into the dump
// extension ("dump" or "sql") and the stored extension ("dump.zst.age",
// ...). encrypted says whether the object must end in ".age".
func storedFormat(objectKey string, encrypted bool) (plainExt, storedExt string, err error) {
	name := objectKey
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	rest := name
	if encrypted {
		var ok bool
		if rest, ok = strings.CutSuffix(rest, ".age"); !ok {
			return "", "", errArtifactName
		}
	} else if strings.HasSuffix(rest, ".age") {
		return "", "", errArtifactName
	}
	rest, ok := strings.CutSuffix(rest, ".zst")
	if !ok {
		return "", "", errArtifactName
	}
	switch {
	case strings.HasSuffix(rest, ".dump"):
		plainExt = "dump"
	case strings.HasSuffix(rest, ".sql"):
		plainExt = "sql"
	default:
		return "", "", errArtifactName
	}
	storedExt = plainExt + ".zst"
	if encrypted {
		storedExt += ".age"
	}
	return plainExt, storedExt, nil
}

// downloadName builds "<job-slug>_<db>_<YYYYMMDDTHHMMSSZ>.<ext>".
func downloadName(jobSlug, db string, ts time.Time, ext string) string {
	return SanitizeFileName(namePart(jobSlug) + "_" + namePart(db) + "_" + ts.UTC().Format("20060102T150405Z") + "." + ext)
}
