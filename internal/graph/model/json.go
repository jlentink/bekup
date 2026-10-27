package model

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/jlentink/bekup/internal/graph/apierror"
)

// MaxJSONInputBytes bounds a JSON scalar input (re-encoded size).
const MaxJSONInputBytes = 32 << 10

// JSON is the JSON scalar: raw JSON passed through unchanged (WebAuthn
// options out, browser responses in; task 1.8). Inputs must be JSON
// objects of at most MaxJSONInputBytes.
type JSON json.RawMessage

// MarshalGQL writes the raw JSON (null when empty).
func (j JSON) MarshalGQL(w io.Writer) {
	if len(j) == 0 || !json.Valid(j) {
		writeString(w, "null")
		return
	}
	writeString(w, string(j))
}

// UnmarshalGQL accepts a JSON object (as decoded from variables or an
// inline literal) and stores its compact encoding.
func (j *JSON) UnmarshalGQL(v any) error {
	obj, ok := v.(map[string]any)
	if !ok {
		return apierror.New(apierror.Validation, "invalid JSON: expected an object")
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return apierror.New(apierror.Validation, "invalid JSON value")
	}
	if len(b) > MaxJSONInputBytes {
		return apierror.Newf(apierror.Validation, "invalid JSON: larger than %d bytes", MaxJSONInputBytes)
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, b); err != nil {
		return apierror.New(apierror.Validation, "invalid JSON value")
	}
	*j = JSON(buf.Bytes())
	return nil
}
