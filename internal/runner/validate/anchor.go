package validate

import (
	"crypto/x509"
	"sync"
	"time"
)

// systemRoots is the system trust store, loaded once (nil when the
// platform has none).
var systemRoots = sync.OnceValue(func() *x509.CertPool {
	p, err := x509.SystemCertPool()
	if err != nil {
		return nil
	}
	return p
})

// IsPublicAnchor reports whether cert is, or chains to, a certificate of
// the system trust store (a public CA). Pin anchors must be a self-signed
// leaf or a private CA (D-133, D-139): a public CA as the native
// client's only trust anchor would accept every certificate that CA
// issues for the host name. The chain is checked at a time inside the
// certificate's own validity, so an expired public certificate is still
// recognized. Without a system trust store it reports false.
func IsPublicAnchor(cert *x509.Certificate) bool {
	return isPublicAnchor(cert, systemRoots())
}

func isPublicAnchor(cert *x509.Certificate, roots *x509.CertPool) bool {
	if cert == nil || roots == nil {
		return false
	}
	at := cert.NotBefore.Add(time.Second)
	if mid := cert.NotBefore.Add(cert.NotAfter.Sub(cert.NotBefore) / 2); mid.After(at) {
		at = mid
	}
	_, err := cert.Verify(x509.VerifyOptions{
		Roots:       roots,
		CurrentTime: at,
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	return err == nil
}
