package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// D-215: the Dockerfile fails the image build when the runtime image lacks
// the system CA bundle setup names for "verify with the system CA bundle"
// or bekup's loader finds no usable certificate in it.
func TestDockerfileChecksSystemCABundle(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		"FROM ${RUNTIME_IMAGE} AS runtime-base",
		"FROM runtime-base AS runtime",
		"--mount=type=bind,from=runtime-base,source=" + SystemCABundle + ",target=/etc/bekup-system-ca/ca-certificates.crt",
		// The real loader (db.LoadSystemCABundle), not a text search.
		"go run ./internal/db/cabundlecheck /etc/bekup-system-ca/ca-certificates.crt",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("Dockerfile lacks %q", want)
		}
	}
}
