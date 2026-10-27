package audit

import (
	"encoding/json"
	"testing"

	"github.com/jlentink/bekup/internal/logging"
)

// TestRedactNonSecretKeyNames: identifier/flag names containing "key"
// (logging.IsSecretKey's exact allowlist) stay readable in audit details,
// while key material is still redacted.
func TestRedactNonSecretKeyNames(t *testing.T) {
	out, err := Redact(Details{
		"host_key_cleared": true, "version_key": "17", "image_key": "postgres-17",
		"ssh_private_key": "-----BEGIN-----", "host_key": "ssh-ed25519 AAAA",
	}, logging.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["host_key_cleared"] != true || got["version_key"] != "17" || got["image_key"] != "postgres-17" {
		t.Fatalf("non-secret names redacted: %v", got)
	}
	if got["ssh_private_key"] != logging.Redacted || got["host_key"] != logging.Redacted {
		t.Fatalf("key material not redacted: %v", got)
	}
}
