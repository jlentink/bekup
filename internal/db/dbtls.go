package db

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/config"
)

// TLSOptions is the TLS policy of every connection to the app DB (D-100):
// the runtime pool and the migrator. The configured mode always wins over
// libpq-style parameters in the connection URL and over PG* environment
// variables.
type TLSOptions struct {
	// Mode is config.DBTLSVerifyFull, config.DBTLSRequire or
	// config.DBTLSDisable. The zero value is rejected.
	Mode string
	// RootCAs is the only trust anchor (never the system roots). Required
	// for verify-full; with require it enables chain verification without
	// a host name check (libpq's verify-ca behavior).
	RootCAs *x509.CertPool
	// Certificates is the optional client certificate.
	Certificates []tls.Certificate
}

// LoadTLSOptions reads the database.tls_* files of a validated config. The
// client key is read with the secret-file rules and never retained beyond
// the parsed tls.Certificate.
func LoadTLSOptions(c *config.Bekup) (TLSOptions, error) {
	d := &c.Database
	o := TLSOptions{Mode: d.TLSMode}
	if d.TLSMode == config.DBTLSDisable {
		return o, nil
	}
	if d.TLSCAFile != "" {
		pemData, err := config.ReadPublicFile(d.TLSCAFile)
		if err != nil {
			return TLSOptions{}, fmt.Errorf("database.tls_ca_file: %w", err)
		}
		cas, err := certs.ParseCertificates(pemData)
		if err != nil {
			return TLSOptions{}, fmt.Errorf("database.tls_ca_file %s: %w", d.TLSCAFile, err)
		}
		o.RootCAs = x509.NewCertPool()
		for _, ca := range cas {
			o.RootCAs.AddCert(ca)
		}
	}
	if d.TLSClientCertFile != "" {
		certPEM, err := config.ReadPublicFile(d.TLSClientCertFile)
		if err != nil {
			return TLSOptions{}, fmt.Errorf("database.tls_client_cert_file: %w", err)
		}
		keyPEM, err := c.DatabaseTLSClientKey()
		if err != nil {
			return TLSOptions{}, fmt.Errorf("database.tls_client_key_file: %w", err)
		}
		pair, err := tls.X509KeyPair(certPEM, keyPEM)
		config.Zero(keyPEM)
		if err != nil {
			// The parser error never contains key material.
			return TLSOptions{}, fmt.Errorf("database.tls_client_cert_file/tls_client_key_file: %w", err)
		}
		o.Certificates = []tls.Certificate{pair}
	}
	return o, o.validate()
}

func (o TLSOptions) validate() error {
	switch o.Mode {
	case config.DBTLSVerifyFull:
		if o.RootCAs == nil {
			return errors.New(`database TLS mode "verify-full" needs a CA (database.tls_ca_file)`)
		}
	case config.DBTLSRequire, config.DBTLSDisable:
	case "":
		return errors.New("database TLS mode not set (database.tls_mode)")
	default:
		return fmt.Errorf("unknown database TLS mode %q", o.Mode)
	}
	return nil
}

// sslModeRank orders libpq sslmode values by strength.
var sslModeRank = map[string]int{
	"disable": 0, "allow": 1, "prefer": 2, "require": 3, "verify-ca": 4, "verify-full": 5,
}

// tlsURLParams are libpq TLS parameters that bekup takes from the config
// only; they are rejected in the URL so that nobody believes they apply.
var tlsURLParams = []string{"sslrootcert", "sslcert", "sslkey", "sslpassword", "sslcertmode", "sslcrl", "sslcrldir"}

// checkURLTLS rejects connection strings that are not postgres:// URLs,
// that carry TLS file parameters, or whose sslmode is stricter than the
// configured mode (an ambiguous intent; a weaker sslmode is simply
// overridden by the configured one). It returns the URL's query parameters.
// Errors never echo the URL.
func checkURLTLS(raw []byte, mode string) (url.Values, error) {
	u, err := url.Parse(string(raw))
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return nil, ErrInvalidURL
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, ErrInvalidURL
	}
	for _, p := range tlsURLParams {
		if q.Has(p) {
			return nil, fmt.Errorf("database URL: parameter %s is not supported; configure TLS with the database.tls_* keys", p)
		}
	}
	if !q.Has("sslmode") {
		return q, nil
	}
	got, ok := sslModeRank[q.Get("sslmode")]
	if !ok {
		return nil, ErrInvalidURL
	}
	if got > sslModeRank[mode] {
		return nil, fmt.Errorf("database URL: sslmode=%s is stricter than database.tls_mode = %q; remove sslmode from the URL or set tls_mode to match", q.Get("sslmode"), mode)
	}
	return q, nil
}

// applyTLS replaces whatever TLS settings pgx derived from the URL or the
// PG* environment with the configured policy, for the primary host and
// every fallback host. pgx's plaintext fallbacks (sslmode=prefer/allow) are
// removed.
//
// With TLS (verify-full, require) SCRAM channel binding is required
// (tls-server-end-point, RFC 5929; D-104): the SCRAM exchange is bound to the
// server certificate the client actually saw, so even an unverified
// `require` connection cannot be relayed by a man in the middle who does not
// know the password. The postgres certificate from `bekup certs` is ECDSA
// P-256 signed with ecdsa-with-SHA256 so the binding hash is defined. A URL
// channel_binding other than "require" is an error, like a conflicting
// sslmode. TLS modes refuse Unix-socket hosts (no TLS there, hence no
// binding); use tls_mode = "disable" for socket connections.
func applyTLS(cc *pgconn.Config, raw []byte, o TLSOptions) error {
	if err := o.validate(); err != nil {
		return err
	}
	q, err := checkURLTLS(raw, o.Mode)
	if err != nil {
		return err
	}
	cb := q.Get("channel_binding")
	if o.Mode == config.DBTLSDisable {
		if cb == "require" {
			return fmt.Errorf("database URL: channel_binding=require needs TLS, but database.tls_mode = %q", o.Mode)
		}
		cc.ChannelBinding = "disable"
	} else {
		if q.Has("channel_binding") && cb != "require" {
			return fmt.Errorf("database URL: channel_binding=%s would weaken the required SCRAM channel binding; remove it from the URL", cb)
		}
		if isSocket(cc.Host) || slices.ContainsFunc(cc.Fallbacks, func(fb *pgconn.FallbackConfig) bool { return isSocket(fb.Host) }) {
			return fmt.Errorf("database URL: a Unix-socket host cannot use TLS; set database.tls_mode = %q for socket connections", config.DBTLSDisable)
		}
		cc.ChannelBinding = "require"
	}
	cc.TLSConfig = o.clientConfig(cc.Host)
	seen := map[string]bool{hostKey(cc.Host, cc.Port): true}
	fallbacks := make([]*pgconn.FallbackConfig, 0, len(cc.Fallbacks))
	for _, fb := range cc.Fallbacks {
		k := hostKey(fb.Host, fb.Port)
		if seen[k] {
			continue
		}
		seen[k] = true
		fallbacks = append(fallbacks, &pgconn.FallbackConfig{Host: fb.Host, Port: fb.Port, TLSConfig: o.clientConfig(fb.Host)})
	}
	cc.Fallbacks = fallbacks
	return nil
}

func isSocket(host string) bool { return strings.HasPrefix(host, "/") }

func hostKey(host string, port uint16) string {
	return net.JoinHostPort(host, strconv.Itoa(int(port)))
}

// clientConfig builds the tls.Config for one host; nil means no TLS.
func (o TLSOptions) clientConfig(host string) *tls.Config {
	if o.Mode == config.DBTLSDisable || isSocket(host) {
		return nil
	}
	c := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: o.Certificates,
		ServerName:   host,
	}
	switch {
	case o.Mode == config.DBTLSVerifyFull:
		c.RootCAs = o.RootCAs
	case o.RootCAs != nil:
		// require + CA: verify the chain, not the host name (verify-ca).
		roots := o.RootCAs
		// Chain verified in VerifyConnection; the host name deliberately not
		// (tls_mode = require, warned at startup).
		c.InsecureSkipVerify = true
		c.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("postgres presented no certificate")
			}
			inter := x509.NewCertPool()
			for _, ic := range cs.PeerCertificates[1:] {
				inter.AddCert(ic)
			}
			_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{
				Roots: roots, Intermediates: inter,
				KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			})
			return err
		}
	default:
		// require without CA: encryption only (warned at startup).
		// tls_mode = require without tls_ca_file: an explicit, warned opt-out
		// (D-100).
		c.InsecureSkipVerify = true
	}
	return c
}
