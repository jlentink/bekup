package model

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/graph/apierror"
)

func TestJSONScalar(t *testing.T) {
	var j JSON
	if err := j.UnmarshalGQL(map[string]any{"id": "abc", "n": json.Number("3"), "nested": map[string]any{"a": []any{true}}}); err != nil {
		t.Fatal(err)
	}
	if string(j) != `{"id":"abc","n":3,"nested":{"a":[true]}}` {
		t.Fatalf("encoded %s", j)
	}
	var buf bytes.Buffer
	j.MarshalGQL(&buf)
	if buf.String() != string(j) {
		t.Fatalf("marshal %s", buf.String())
	}
	for _, bad := range []any{"{}", []any{}, 3, nil, map[string]any{"x": strings.Repeat("a", MaxJSONInputBytes)}} {
		var k JSON
		if err := k.UnmarshalGQL(bad); !apierror.IsCode(err, apierror.Validation) {
			t.Errorf("UnmarshalGQL(%.20v) = %v", bad, err)
		}
	}
	buf.Reset()
	JSON(nil).MarshalGQL(&buf)
	if buf.String() != "null" {
		t.Fatalf("empty marshal %q", buf.String())
	}
}
