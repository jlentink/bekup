package s3store

import (
	"testing"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/netguard"
)

func TestConfigValidateExported(t *testing.T) {
	g, err := netguard.New(netguard.Options{})
	if err != nil {
		t.Fatal(err)
	}
	c := Config{Region: "us-east-1", Bucket: "bucket", AccessKeyID: "AKIAEXAMPLE", SecretAccessKey: crypto.NewSecret([]byte("s")), Guard: g}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.Checksum != "" || c.PartSize != 0 {
		t.Fatal("Validate filled defaults into the caller's config")
	}
	c.Endpoint = "http://minio:9000"
	if err := c.Validate(); err == nil {
		t.Fatal("http endpoint accepted without AllowHTTP")
	}
	c.AllowHTTP = true
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Guard = nil
	if err := c.Validate(); err == nil {
		t.Fatal("missing guard accepted")
	}
}
