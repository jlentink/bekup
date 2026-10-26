package certs

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jlentink/bekup/internal/config"
)

// ExpiryInfo describes one certificate for inspection, metrics and alerts
// (tasks 5.5/5.6).
type ExpiryInfo struct {
	// Path is the file the certificate was read from; Index its position in
	// that file (bundles hold several).
	Path  string
	Index int

	Subject   string
	Issuer    string
	Serial    string
	IsCA      bool
	DNSNames  []string
	URIs      []string
	IPs       []string
	NotBefore time.Time
	NotAfter  time.Time
	// SHA256 is the certificate fingerprint (hex), SPKISHA256 the base64
	// SHA-256 of the SubjectPublicKeyInfo (pin format).
	SHA256     string
	SPKISHA256 string
}

// NewExpiryInfo builds an ExpiryInfo from a parsed certificate.
func NewExpiryInfo(path string, index int, c *x509.Certificate) ExpiryInfo {
	fp := sha256.Sum256(c.Raw)
	spki := sha256.Sum256(c.RawSubjectPublicKeyInfo)
	e := ExpiryInfo{
		Path:       path,
		Index:      index,
		Subject:    c.Subject.String(),
		Issuer:     c.Issuer.String(),
		Serial:     hex.EncodeToString(c.SerialNumber.Bytes()),
		IsCA:       c.IsCA,
		DNSNames:   c.DNSNames,
		NotBefore:  c.NotBefore.UTC(),
		NotAfter:   c.NotAfter.UTC(),
		SHA256:     hex.EncodeToString(fp[:]),
		SPKISHA256: base64.StdEncoding.EncodeToString(spki[:]),
	}
	for _, u := range c.URIs {
		e.URIs = append(e.URIs, u.String())
	}
	for _, ip := range c.IPAddresses {
		e.IPs = append(e.IPs, ip.String())
	}
	return e
}

// Remaining is the time left until NotAfter (negative once expired).
func (e ExpiryInfo) Remaining(now time.Time) time.Duration { return e.NotAfter.Sub(now) }

// DaysLeft is the number of whole days until expiry (negative once
// expired).
func (e ExpiryInfo) DaysLeft(now time.Time) int {
	d := e.Remaining(now)
	days := int(d / (24 * time.Hour))
	if d < 0 && d%(24*time.Hour) != 0 {
		days--
	}
	return days
}

// Expired reports whether the certificate is past NotAfter.
func (e ExpiryInfo) Expired(now time.Time) bool { return !now.Before(e.NotAfter) }

// NotYetValid reports whether the certificate is before NotBefore.
func (e ExpiryInfo) NotYetValid(now time.Time) bool { return now.Before(e.NotBefore) }

// ExpiresWithin reports whether the certificate is invalid now or expires
// within d.
func (e ExpiryInfo) ExpiresWithin(now time.Time, d time.Duration) bool {
	return e.NotYetValid(now) || !now.Add(d).Before(e.NotAfter)
}

// SANs returns all subject alternative names as strings.
func (e ExpiryInfo) SANs() []string {
	out := make([]string, 0, len(e.DNSNames)+len(e.URIs)+len(e.IPs))
	for _, n := range e.DNSNames {
		out = append(out, "DNS:"+n)
	}
	for _, n := range e.IPs {
		out = append(out, "IP:"+n)
	}
	for _, n := range e.URIs {
		out = append(out, "URI:"+n)
	}
	return out
}

// InspectFile parses every certificate in a PEM file.
func InspectFile(path string) ([]ExpiryInfo, error) {
	data, err := config.ReadPublicFile(path)
	if err != nil {
		return nil, err
	}
	certs, err := ParseCertificates(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	out := make([]ExpiryInfo, 0, len(certs))
	for i, c := range certs {
		out = append(out, NewExpiryInfo(path, i, c))
	}
	return out, nil
}

// Inspect inspects a file, or every *.crt file below a directory (symlinks
// are not followed), in lexical path order.
func Inspect(path string) ([]ExpiryInfo, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return InspectFile(path)
	}
	var files []string
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() && strings.HasSuffix(d.Name(), ".crt") && !strings.HasPrefix(d.Name(), ".") {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var out []ExpiryInfo
	for _, f := range files {
		infos, err := InspectFile(f)
		if err != nil {
			return nil, err
		}
		out = append(out, infos...)
	}
	return out, nil
}

// Expiring returns the entries that are invalid now or expire within d.
func Expiring(infos []ExpiryInfo, now time.Time, d time.Duration) []ExpiryInfo {
	var out []ExpiryInfo
	for i := range infos {
		if infos[i].ExpiresWithin(now, d) {
			out = append(out, infos[i])
		}
	}
	return out
}
