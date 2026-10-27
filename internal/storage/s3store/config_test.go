package s3store

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/storage"
)

func testGuard(t *testing.T) *netguard.Guard {
	t.Helper()
	g, err := netguard.New(netguard.Options{LoopbackAllowable: true, AllowCIDRs: []string{"127.0.0.0/8"}})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func baseConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		Endpoint:        "https://s3.example.com",
		Region:          "eu-central-1",
		Bucket:          "bekup-backups",
		AccessKeyID:     "AKIAEXAMPLE",
		SecretAccessKey: crypto.NewSecret([]byte("example-key-material")),
		Guard:           testGuard(t),
	}
}

func TestProductionDefaults(t *testing.T) {
	if DefaultPartSize < 64<<20 {
		t.Fatalf("default part size %d < 64 MiB", DefaultPartSize)
	}
	cfg := baseConfig(t)
	if _, err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.PartSize < 64<<20 || cfg.Concurrency != DefaultConcurrency || cfg.Checksum != ChecksumWhenSupported || !cfg.conditional() {
		t.Fatalf("defaults = part %d, concurrency %d, checksum %s, conditional %v",
			cfg.PartSize, cfg.Concurrency, cfg.Checksum, cfg.conditional())
	}
	if got := cfg.maxObjectSize(); got != DefaultPartSize*MaxParts {
		t.Fatalf("max object size = %d", got)
	}
}

func TestConfigValidation(t *testing.T) {
	f := false
	tests := []struct {
		name string
		mod  func(*Config)
		ok   bool
	}{
		{"base", func(*Config) {}, true},
		{"aws default endpoint", func(c *Config) { c.Endpoint = "" }, true},
		{"trailing slash", func(c *Config) { c.Endpoint = "https://minio.internal:9000/" }, true},
		{"http refused", func(c *Config) { c.Endpoint = "http://minio:9000" }, false},
		{"http dev", func(c *Config) { c.Endpoint = "http://minio:9000"; c.AllowHTTP = true }, true},
		{"ftp", func(c *Config) { c.Endpoint = "ftp://x" }, false},
		{"userinfo", func(c *Config) { c.Endpoint = "https://u:p@s3.example.com" }, false},
		{"path", func(c *Config) { c.Endpoint = "https://s3.example.com/bucket" }, false},
		{"query", func(c *Config) { c.Endpoint = "https://s3.example.com?x=1" }, false},
		{"no host", func(c *Config) { c.Endpoint = "https://" }, false},
		{"region", func(c *Config) { c.Region = "EU Central" }, false},
		{"bucket upper", func(c *Config) { c.Bucket = "Bucket" }, false},
		{"bucket slash", func(c *Config) { c.Bucket = "a/b" }, false},
		{"bucket short", func(c *Config) { c.Bucket = "ab" }, false},
		{"bucket ip", func(c *Config) { c.Bucket = "10.0.0.1" }, false},
		{"bucket dots", func(c *Config) { c.Bucket = "a..b" }, false},
		{"prefix", func(c *Config) { c.Prefix = "tenant/a/" }, true},
		{"prefix traversal", func(c *Config) { c.Prefix = "../x" }, false},
		{"no key id", func(c *Config) { c.AccessKeyID = "" }, false},
		{"key id space", func(c *Config) { c.AccessKeyID = "AK IA" }, false},
		{"no secret", func(c *Config) { c.SecretAccessKey = nil }, false},
		{"no guard", func(c *Config) { c.Guard = nil }, false},
		{"bad ca", func(c *Config) { c.CABundlePEM = []byte("not pem") }, false},
		{"sse-s3", func(c *Config) { c.SSE = SSES3 }, true},
		{"sse-kms", func(c *Config) { c.SSE = SSEKMS; c.KMSKeyID = "arn:aws:kms:eu-central-1:111122223333:key/abc" }, true},
		{"kms key without kms", func(c *Config) { c.SSE = SSES3; c.KMSKeyID = "k" }, false},
		{"kms key bad", func(c *Config) { c.SSE = SSEKMS; c.KMSKeyID = "a b" }, false},
		{"sse unknown", func(c *Config) { c.SSE = "rot13" }, false},
		{"class ia", func(c *Config) { c.StorageClass = "STANDARD_IA" }, true},
		{"class glacier", func(c *Config) { c.StorageClass = "GLACIER" }, false},
		{"class deep archive", func(c *Config) { c.StorageClass = "DEEP_ARCHIVE" }, false},
		{"checksum required", func(c *Config) { c.Checksum = ChecksumWhenRequired }, true},
		{"checksum bad", func(c *Config) { c.Checksum = "always" }, false},
		{"lock", func(c *Config) { c.LockMode = storage.LockCompliance; c.RetentionDays = 30 }, true},
		{"lock no days", func(c *Config) { c.LockMode = storage.LockGovernance }, false},
		{"days no lock", func(c *Config) { c.RetentionDays = 3 }, false},
		{"lock too long", func(c *Config) { c.LockMode = storage.LockGovernance; c.RetentionDays = MaxRetentionDays + 1 }, false},
		{"lock bad", func(c *Config) { c.LockMode = "forever"; c.RetentionDays = 1 }, false},
		{"lock and no-delete", func(c *Config) { c.LockMode = storage.LockGovernance; c.RetentionDays = 1; c.NoDelete = true }, true},
		{"part min", func(c *Config) { c.PartSize = MinPartSize }, true},
		{"part small", func(c *Config) { c.PartSize = MinPartSize - 1 }, false},
		{"part big", func(c *Config) { c.PartSize = MaxPartSize + 1 }, false},
		{"concurrency", func(c *Config) { c.Concurrency = MaxConcurrency + 1 }, false},
		{"concurrency negative", func(c *Config) { c.Concurrency = -1 }, false},
		{"unconditional", func(c *Config) { c.ConditionalWrites = &f }, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseConfig(t)
			tc.mod(&cfg)
			_, err := cfg.validate()
			if (err == nil) != tc.ok {
				t.Fatalf("validate = %v, want ok=%v", err, tc.ok)
			}
			if err != nil && !errors.Is(err, storage.ErrInvalidArgument) {
				t.Fatalf("error %v does not wrap ErrInvalidArgument", err)
			}
		})
	}
}

func TestConfigNeverPrintsSecret(t *testing.T) {
	cfg := baseConfig(t)
	for _, s := range []string{fmt.Sprintf("%v", cfg), fmt.Sprintf("%+v", cfg), fmt.Sprintf("%#v", cfg)} {
		if strings.Contains(s, "example-key-material") {
			t.Fatalf("secret printed: %s", s)
		}
		if !strings.Contains(s, logging.Redacted) {
			t.Fatalf("secret not redacted: %s", s)
		}
	}
	cfg.Bucket = "INVALID"
	cfg.Endpoint = "https://u:p@x"
	_, err := cfg.validate()
	if err == nil || strings.Contains(err.Error(), "example-key-material") || strings.Contains(err.Error(), "u:p") {
		t.Fatalf("validate error = %v", err)
	}
}

func FuzzParseEndpoint(f *testing.F) {
	for _, s := range []string{"https://s3.example.com", "http://minio:9000", "https://u@x", "https://x/y?z#w", "https://[::1]:9000", ""} {
		f.Add(s, false)
	}
	f.Fuzz(func(t *testing.T, s string, allowHTTP bool) {
		u, err := parseEndpoint(s, allowHTTP)
		if err != nil {
			return
		}
		if u.Scheme != "https" && (u.Scheme != "http" || !allowHTTP) {
			t.Fatalf("accepted scheme %q", u.Scheme)
		}
		if u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Hostname() == "" {
			t.Fatalf("accepted %q", s)
		}
	})
}
