package jobs

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/scheduler"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// Limits of user input (mirrored by CHECK constraints in migration 00015).
const (
	MaxNameRunes        = 100
	MaxDescriptionRunes = 1000
	MaxSlugLen          = 63
	MaxDatabases        = 256
	MaxDestinations     = 16
	MaxOwners           = 50
	MaxKeepLast         = 10000
	MaxKeepDays         = 36500
	MinTimeout          = time.Minute
	// HardMaxTimeout is the column limit; the effective cap is
	// workers.backup_timeout.
	HardMaxTimeout    = 7 * 24 * time.Hour
	MinExpectedWindow = time.Minute
	MaxExpectedWindow = 366 * 24 * time.Hour
	// DefaultPreview / scheduler.MaxPreview bound PreviewSchedule.
	DefaultPreview = 5
)

func fieldErr(field, format string, a ...any) error { return serviceutil.FieldErr(field, format, a...) }

// cleanText trims s and checks its length (in runes) and that it has no
// control characters (newlines allowed when multiline).
func cleanText(field, s string, minRunes, maxRunes int, multiline bool) (string, error) {
	s = strings.TrimSpace(s)
	if !utf8.ValidString(s) {
		return "", fieldErr(field, "must be valid UTF-8")
	}
	n := utf8.RuneCountInString(s)
	if n < minRunes {
		return "", fieldErr(field, "must not be empty")
	}
	if n > maxRunes {
		return "", fieldErr(field, "must be at most %d characters", maxRunes)
	}
	for _, r := range s {
		if multiline && (r == '\n' || r == '\t') {
			continue
		}
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return "", fieldErr(field, "must not contain control characters")
		}
	}
	return s, nil
}

func normalizeName(s string) (string, error) { return cleanText("name", s, 1, MaxNameRunes, false) }

func normalizeDescription(s string) (string, error) {
	return cleanText("description", s, 0, MaxDescriptionRunes, true)
}

// validSlug reports whether s matches the jobs_slug_format CHECK: lower-case
// letters, digits and inner hyphens, 1..63 characters.
func validSlug(s string) bool {
	if s == "" || len(s) > MaxSlugLen || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

func checkSlug(s string) (string, error) {
	if !validSlug(s) {
		return "", fieldErr("slug", "use 1-%d lower-case letters, digits and inner hyphens", MaxSlugLen)
	}
	return s, nil
}

// slugFromName derives a slug: ASCII letters and digits lower-cased, every
// other run of characters a single hyphen, trimmed to MaxSlugLen. Names
// without any ASCII letter or digit give "job".
func slugFromName(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		default:
			dash = true
		}
		if b.Len() >= MaxSlugLen {
			break
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > MaxSlugLen {
		s = strings.TrimRight(s[:MaxSlugLen], "-")
	}
	if s == "" {
		return "job"
	}
	return s
}

// withSuffix returns base with "-n", shortened so the result fits.
func withSuffix(base string, n int) string {
	suffix := "-" + strconv.Itoa(n)
	if len(base)+len(suffix) > MaxSlugLen {
		base = strings.TrimRight(base[:MaxSlugLen-len(suffix)], "-")
	}
	return base + suffix
}

// checkDatabaseList validates a database name list: engine charset, no
// duplicates, at most MaxDatabases. The result is sorted.
func checkDatabaseList(field string, names []string) ([]string, error) {
	if len(names) > MaxDatabases {
		return nil, fieldErr(field, "at most %d databases", MaxDatabases)
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if err := engine.ValidateDBName(n); err != nil {
			return nil, fieldErr(field, "%q is not a valid database name", truncate(n))
		}
		if slices.Contains(out, n) {
			return nil, fieldErr(field, "%q is listed twice", n)
		}
		out = append(out, n)
	}
	slices.Sort(out)
	return out, nil
}

func truncate(s string) string {
	const maxLen = 70
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// selection is the validated database selection of a job.
type selection struct {
	mode      DBMode
	databases []string
	exclude   []string
}

func checkSelection(mode DBMode, databases, exclude []string) (selection, error) {
	if !mode.Valid() {
		return selection{}, fieldErr("dbMode", "must be ALL or SELECTED")
	}
	dbs, err := checkDatabaseList("databases", databases)
	if err != nil {
		return selection{}, err
	}
	ex, err := checkDatabaseList("exclude", exclude)
	if err != nil {
		return selection{}, err
	}
	switch mode {
	case DBModeAll:
		if len(dbs) > 0 {
			return selection{}, fieldErr("databases", "list databases only with SELECTED (use exclude with ALL)")
		}
	case DBModeSelected:
		if len(dbs) == 0 {
			return selection{}, fieldErr("databases", "select at least one database")
		}
		if len(ex) > 0 {
			return selection{}, fieldErr("exclude", "exclude applies only to ALL")
		}
	}
	return selection{mode: mode, databases: dbs, exclude: ex}, nil
}

func checkRetention(r Retention) error {
	if r.KeepLast == nil && r.KeepDays == nil {
		return fieldErr("retention", "set keepLast, keepDays or both")
	}
	if r.KeepLast != nil && (*r.KeepLast < 1 || *r.KeepLast > MaxKeepLast) {
		return fieldErr("retention.keepLast", "must be between 1 and %d", MaxKeepLast)
	}
	if r.KeepDays != nil && (*r.KeepDays < 1 || *r.KeepDays > MaxKeepDays) {
		return fieldErr("retention.keepDays", "must be between 1 and %d", MaxKeepDays)
	}
	return nil
}

func checkTimeout(d, maxTimeout time.Duration) error {
	if d < MinTimeout || d > maxTimeout || d%time.Second != 0 {
		return fieldErr("timeoutSeconds", "must be whole seconds between %d and %d (workers.backup_timeout)",
			int(MinTimeout/time.Second), int(maxTimeout/time.Second))
	}
	return nil
}

func checkExpectedWindow(d time.Duration) error {
	if d < MinExpectedWindow || d > MaxExpectedWindow || d%time.Second != 0 {
		return fieldErr("expectedWindowSeconds", "must be whole seconds between %d and %d",
			int(MinExpectedWindow/time.Second), int(MaxExpectedWindow/time.Second))
	}
	return nil
}

// checkSchedule normalizes s and applies the configurable policy
// (scheduler.min_interval, D-143).
func checkSchedule(s scheduler.Schedule, lim scheduler.Limits) (scheduler.Schedule, []byte, error) {
	n, err := s.Normalize()
	if err == nil {
		err = n.CheckLimits(lim)
	}
	if err != nil {
		return scheduler.Schedule{}, nil, scheduleErr(err)
	}
	enc, err := n.Encode()
	if err != nil {
		return scheduler.Schedule{}, nil, scheduleErr(err)
	}
	return n, enc, nil
}

// scheduleErr turns a scheduler validation error into a field error; the
// scheduler's messages describe the input only.
func scheduleErr(err error) error {
	if errors.Is(err, scheduler.ErrInvalid) {
		return fieldErr("schedule", "%s", strings.TrimPrefix(err.Error(), scheduler.ErrInvalid.Error()+": "))
	}
	if errors.Is(err, scheduler.ErrNoOccurrence) || errors.Is(err, scheduler.ErrOutOfRange) {
		return fieldErr("schedule", "the schedule has no future occurrence")
	}
	return err
}

func checkDestinations(ids []uuid.UUID) error {
	if len(ids) == 0 {
		return fieldErr("destinationIds", "select at least one destination")
	}
	if len(ids) > MaxDestinations {
		return fieldErr("destinationIds", "at most %d destinations", MaxDestinations)
	}
	for i, id := range ids {
		if id == uuid.Nil {
			return fieldErr("destinationIds", "invalid destination")
		}
		if slices.Contains(ids[:i], id) {
			return fieldErr("destinationIds", "a destination is listed twice")
		}
	}
	return nil
}
