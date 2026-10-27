package audit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jlentink/bekup/internal/logging"
)

// MaxDetailsBytes is the audit_log details limit (jsonb text form; the
// encoded JSON is checked, which is close to it).
const MaxDetailsBytes = 64 << 10

// maxDetailsDepth bounds the nesting of details.
const maxDetailsDepth = 16

// Redact returns details as JSON with secrets removed: every value under a
// key that names a secret (logging.IsSecretKey: password, token, key, ...)
// is replaced by logging.Redacted, and every string has credentials in URLs
// and key=value strings (logging.RedactCredentials) and values registered in
// reg (logging.DefaultRegistry when nil) scrubbed. NUL characters, which
// jsonb rejects, become U+FFFD. Values are normalized through JSON first, so
// structs are redacted by their JSON field names.
func Redact(details Details, reg *logging.Registry) (json.RawMessage, error) {
	if reg == nil {
		reg = logging.DefaultRegistry
	}
	if len(details) == 0 {
		return json.RawMessage(`{}`), nil
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return nil, fmt.Errorf("encode audit details: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return nil, fmt.Errorf("decode audit details: %w", err)
	}
	clean, err := redactValue(tree, reg, 0)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(clean)
	if err != nil {
		return nil, fmt.Errorf("encode audit details: %w", err)
	}
	if len(out) > MaxDetailsBytes {
		return nil, fmt.Errorf("%w: %d bytes (max %d)", ErrDetailsTooLarge, len(out), MaxDetailsBytes)
	}
	return out, nil
}

func redactValue(v any, reg *logging.Registry, depth int) (any, error) {
	if depth > maxDetailsDepth {
		return nil, fmt.Errorf("%w: nested deeper than %d levels", ErrInvalidEvent, maxDetailsDepth)
	}
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			key := redactString(k, reg)
			if logging.IsSecretKey(k) {
				out[key] = logging.Redacted
				continue
			}
			r, err := redactValue(val, reg, depth+1)
			if err != nil {
				return nil, err
			}
			out[key] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			r, err := redactValue(val, reg, depth+1)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	case string:
		return redactString(t, reg), nil
	default: // json.Number, bool, nil
		return t, nil
	}
}

func redactString(s string, reg *logging.Registry) string {
	s = reg.Scrub(logging.RedactCredentials(s))
	return strings.ReplaceAll(s, "\x00", "�")
}
