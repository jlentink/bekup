package pipeline

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"unicode/utf8"
)

// Canonical JSON (used for the signed manifest bytes):
//
//   - UTF-8 only (invalid UTF-8 is rejected), no insignificant whitespace,
//     no trailing data;
//   - object members sorted by key in byte-wise order of the UTF-8 key;
//     duplicate keys are rejected;
//   - strings encoded like Go's encoding/json with HTML escaping off:
//     `"` and `\` as `\"` and `\\`; U+0008, U+000C, U+000A, U+000D, U+0009
//     as `\b`, `\f`, `\n`, `\r`, `\t`; other control characters as
//     `\u00xx` (lowercase hex); U+2028 and U+2029 as `\u2028` and `\u2029`;
//     every other character literally;
//   - numbers must be integers in the int64 range, written without sign
//     for non-negative values, without leading zeros, fraction or exponent
//     ("-0" is rejected). Anything else must be a string;
//   - true, false and null literally;
//   - nesting depth at most maxJSONDepth.
//
// The verifier re-canonicalizes the embedded manifest before checking the
// signature, so re-formatting manifest.json does not break verification,
// while every semantic change does.

const maxJSONDepth = 32

var (
	// errNotCanonicalizable reports input that is not valid canonical-able JSON.
	errNotCanonicalizable = errors.New("not canonicalizable JSON")
	intRe                 = regexp.MustCompile(`^-?(0|[1-9]\d*)$`)
)

// jsonObject is a parsed JSON object preserving uniqueness of keys.
type jsonObject map[string]any

// Canonicalize returns the canonical form of a JSON document.
func Canonicalize(data []byte) ([]byte, error) {
	v, err := parseJSON(data)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func parseJSON(data []byte) (any, error) {
	if len(data) > MaxManifestBytes {
		return nil, fmt.Errorf("%w: larger than %d bytes", errNotCanonicalizable, MaxManifestBytes)
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("%w: invalid UTF-8", errNotCanonicalizable)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := parseValue(dec, 0)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: trailing data", errNotCanonicalizable)
	}
	return v, nil
}

func parseValue(dec *json.Decoder, depth int) (any, error) {
	if depth > maxJSONDepth {
		return nil, fmt.Errorf("%w: nested deeper than %d", errNotCanonicalizable, maxJSONDepth)
	}
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errNotCanonicalizable, err)
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := jsonObject{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, fmt.Errorf("%w: %w", errNotCanonicalizable, err)
				}
				k, ok := kt.(string)
				if !ok {
					return nil, fmt.Errorf("%w: object key is not a string", errNotCanonicalizable)
				}
				if _, dup := obj[k]; dup {
					return nil, fmt.Errorf("%w: duplicate key %q", errNotCanonicalizable, truncateForError(k))
				}
				v, err := parseValue(dec, depth+1)
				if err != nil {
					return nil, err
				}
				obj[k] = v
			}
			if _, err := dec.Token(); err != nil { // '}'
				return nil, fmt.Errorf("%w: %w", errNotCanonicalizable, err)
			}
			return obj, nil
		case '[':
			arr := []any{}
			for dec.More() {
				v, err := parseValue(dec, depth+1)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			if _, err := dec.Token(); err != nil { // ']'
				return nil, fmt.Errorf("%w: %w", errNotCanonicalizable, err)
			}
			return arr, nil
		default:
			return nil, fmt.Errorf("%w: unexpected delimiter", errNotCanonicalizable)
		}
	case json.Number:
		if !intRe.MatchString(string(t)) || string(t) == "-0" {
			return nil, fmt.Errorf("%w: numbers must be integers", errNotCanonicalizable)
		}
		if _, err := strconv.ParseInt(string(t), 10, 64); err != nil {
			return nil, fmt.Errorf("%w: integer out of range", errNotCanonicalizable)
		}
		return t, nil
	case string, bool, nil:
		return t, nil
	default:
		return nil, fmt.Errorf("%w: unexpected token", errNotCanonicalizable)
	}
}

func writeCanonical(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case jsonObject:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		slices.Sort(keys) // Go string order is byte-wise
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeString(buf, k); err != nil {
				return err
			}
			buf.WriteByte(':')
			if err := writeCanonical(buf, t[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case string:
		return writeString(buf, t)
	case json.Number:
		buf.WriteString(string(t))
	case bool:
		buf.WriteString(strconv.FormatBool(t))
	case nil:
		buf.WriteString("null")
	default:
		return fmt.Errorf("%w: unexpected value", errNotCanonicalizable)
	}
	return nil
}

func writeString(buf *bytes.Buffer, s string) error {
	var tmp bytes.Buffer
	enc := json.NewEncoder(&tmp)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return fmt.Errorf("%w: %w", errNotCanonicalizable, err)
	}
	buf.Write(bytes.TrimSuffix(tmp.Bytes(), []byte{'\n'}))
	return nil
}

func truncateForError(s string) string {
	if len(s) > 32 {
		return s[:32] + "..."
	}
	return s
}
