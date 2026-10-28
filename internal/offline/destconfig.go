package offline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/storage"
	"github.com/jlentink/bekup/internal/storage/s3store"
	"github.com/jlentink/bekup/internal/storage/sftp"
)

// DestinationConfig is the offline description of a destination for
// `bekup reindex` (docs/recovery.md). Credentials are only ever *_file
// paths, read with config.ReadSecretFile; the destination is always opened
// in no-delete mode and reindex never writes to it.
type DestinationConfig struct {
	// Type is "disk", "s3" or "sftp".
	Type string        `toml:"type"`
	Disk *DiskSection  `toml:"disk"`
	S3   *S3Section    `toml:"s3"`
	SFTP *SFTPSection  `toml:"sftp"`
	Net  *GuardSection `toml:"netguard"`
}

// DiskSection describes a disk destination.
type DiskSection struct {
	// Path is the absolute directory that holds the {job-slug}/
	// directories (storage.disk_root joined with the destination's path).
	Path string `toml:"path"`
}

// S3Section describes an S3 destination.
type S3Section struct {
	Endpoint            string `toml:"endpoint"`
	Region              string `toml:"region"`
	Bucket              string `toml:"bucket"`
	Prefix              string `toml:"prefix"`
	PathStyle           bool   `toml:"path_style"`
	AccessKeyID         string `toml:"access_key_id"`
	SecretAccessKeyFile string `toml:"secret_access_key_file"`
	CABundleFile        string `toml:"ca_bundle_file"`
	Checksum            string `toml:"checksum"`
	// AllowHTTP permits an http:// endpoint (test setups only; logged).
	AllowHTTP bool `toml:"allow_http"`
}

// SFTPSection describes an SFTP destination.
type SFTPSection struct {
	Host     string `toml:"host"`
	Port     int    `toml:"port"`
	User     string `toml:"user"`
	BasePath string `toml:"base_path"`
	Dir      string `toml:"dir"`
	// HostKeyType / HostKeyFingerprint ("SHA256:...") or
	// HostKeyAuthorizedKey pin the server's host key (mandatory).
	HostKeyType          string `toml:"host_key_type"`
	HostKeyFingerprint   string `toml:"host_key_fingerprint"`
	HostKeyAuthorizedKey string `toml:"host_key_authorized_key"`
	PrivateKeyFile       string `toml:"private_key_file"`
	PassphraseFile       string `toml:"private_key_passphrase_file"`
	PasswordFile         string `toml:"password_file"`
}

// GuardSection is the SSRF guard allowlist for the destination's host
// (private addresses are blocked unless listed, as in [netguard]).
type GuardSection struct {
	AllowCIDRs []string `toml:"allow_cidrs"`
	AllowHosts []string `toml:"allow_hosts"`
}

// LoadDestinationConfig reads and validates a destination description
// (file safety rules of config.ReadPublicFile; strict TOML: unknown keys
// are errors).
func LoadDestinationConfig(path string) (*DestinationConfig, error) {
	data, err := config.ReadPublicFile(path)
	if err != nil {
		return nil, fmt.Errorf("destination config: %w", err)
	}
	return ParseDestinationConfig(data)
}

// ParseDestinationConfig parses a destination description.
func ParseDestinationConfig(data []byte) (*DestinationConfig, error) {
	var c DestinationConfig
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		var sm *toml.StrictMissingError
		if errors.As(err, &sm) {
			return nil, fmt.Errorf("destination config: %s", sm.String())
		}
		return nil, fmt.Errorf("destination config: %w", err)
	}
	sections := 0
	for _, set := range []bool{c.Disk != nil, c.S3 != nil, c.SFTP != nil} {
		if set {
			sections++
		}
	}
	if sections != 1 {
		return nil, errors.New("destination config: exactly one of [disk], [s3] or [sftp] is required")
	}
	switch {
	case c.Type == "disk" && c.Disk != nil:
		if !filepath.IsAbs(c.Disk.Path) {
			return nil, errors.New("destination config: disk.path must be an absolute directory")
		}
	case c.Type == "s3" && c.S3 != nil:
		if c.S3.SecretAccessKeyFile == "" {
			return nil, errors.New("destination config: s3.secret_access_key_file is required")
		}
	case c.Type == "sftp" && c.SFTP != nil:
		n := 0
		for _, f := range []string{c.SFTP.PrivateKeyFile, c.SFTP.PasswordFile} {
			if f != "" {
				n++
			}
		}
		if n != 1 {
			return nil, errors.New("destination config: set exactly one of sftp.private_key_file or sftp.password_file")
		}
		if c.SFTP.PassphraseFile != "" && c.SFTP.PrivateKeyFile == "" {
			return nil, errors.New("destination config: sftp.private_key_passphrase_file needs sftp.private_key_file")
		}
	default:
		return nil, errors.New(`destination config: type must be "disk", "s3" or "sftp" and match its section`)
	}
	return &c, nil
}

func readSecret(path, what string) (*crypto.Secret, error) {
	b, err := config.ReadSecretFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	return crypto.NewSecret(b), nil
}

// Open opens the destination read-only (no-delete mode; reindex only
// lists and reads). Network destinations dial through an SSRF guard built
// from [netguard].
func (c *DestinationConfig) Open(ctx context.Context, logger *slog.Logger) (storage.Destination, error) {
	if logger == nil {
		logger = slog.Default()
	}
	var guard *netguard.Guard
	if c.Type != "disk" {
		opts := netguard.Options{}
		if c.Net != nil {
			opts.AllowCIDRs, opts.AllowHosts = c.Net.AllowCIDRs, c.Net.AllowHosts
		}
		g, err := netguard.New(opts)
		if err != nil {
			return nil, fmt.Errorf("destination config: netguard: %w", err)
		}
		guard = g
	}
	switch c.Type {
	case "disk":
		return storage.OpenDisk(storage.DiskConfig{Root: c.Disk.Path, NoDelete: true})
	case "s3":
		return c.openS3(ctx, guard, logger)
	case "sftp":
		return c.openSFTP(guard, logger)
	}
	return nil, errors.New("destination config: unknown type")
}

func (c *DestinationConfig) openS3(ctx context.Context, guard *netguard.Guard, logger *slog.Logger) (storage.Destination, error) {
	s := c.S3
	key, err := readSecret(s.SecretAccessKeyFile, "s3.secret_access_key_file")
	if err != nil {
		return nil, err
	}
	cfg := s3store.Config{
		Endpoint: s.Endpoint, Region: s.Region, Bucket: s.Bucket, Prefix: s.Prefix, PathStyle: s.PathStyle,
		AccessKeyID: s.AccessKeyID, SecretAccessKey: key, Checksum: s3store.ChecksumMode(s.Checksum),
		NoDelete: true, AllowHTTP: s.AllowHTTP, Guard: guard, Logger: logger,
	}
	if s.CABundleFile != "" {
		pem, err := config.ReadPublicFile(s.CABundleFile)
		if err != nil {
			key.Destroy()
			return nil, fmt.Errorf("s3.ca_bundle_file: %w", err)
		}
		cfg.CABundlePEM = pem
	}
	d, err := s3store.Open(ctx, cfg)
	if err != nil {
		key.Destroy()
		return nil, err
	}
	return &closeHook{Destination: d, after: key.Destroy}, nil
}

func (c *DestinationConfig) openSFTP(guard *netguard.Guard, logger *slog.Logger) (storage.Destination, error) {
	s := c.SFTP
	var auth sftp.Auth
	var err error
	if s.PrivateKeyFile != "" {
		if auth.PrivateKey, err = readSecret(s.PrivateKeyFile, "sftp.private_key_file"); err != nil {
			return nil, err
		}
		if s.PassphraseFile != "" {
			if auth.Passphrase, err = readSecret(s.PassphraseFile, "sftp.private_key_passphrase_file"); err != nil {
				auth.PrivateKey.Destroy()
				return nil, err
			}
		}
	} else if auth.Password, err = readSecret(s.PasswordFile, "sftp.password_file"); err != nil {
		return nil, err
	}
	// sftp.Open takes ownership of the secrets (and destroys them).
	return sftp.Open(sftp.Config{
		Host: s.Host, Port: s.Port, User: s.User, Auth: auth,
		HostKeyPin: sftp.HostKeyPin{Type: s.HostKeyType, FingerprintSHA256: s.HostKeyFingerprint, AuthorizedKey: s.HostKeyAuthorizedKey},
		BasePath:   s.BasePath, Dir: s.Dir, NoDelete: true, Guard: guard, Logger: logger,
	})
}

// closeHook runs after once the wrapped destination is closed.
type closeHook struct {
	storage.Destination
	after func()
}

func (c *closeHook) Close() error {
	err := c.Destination.Close()
	if c.after != nil {
		c.after()
		c.after = nil
	}
	return err
}
