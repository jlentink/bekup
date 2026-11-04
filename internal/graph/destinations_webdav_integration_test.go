//go:build integration

package graph_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/storage/webdav"
	"github.com/jlentink/bekup/internal/storage/webdav/webdavtest"
)

const (
	webdavDestFields = `id type ready notReadyReason settings { webdav { url flavor username path caBundlePem timeoutSeconds hasPassword trust
		pinnedCertificate { spkiSha256 spkiSha256Base64 fingerprintSha256 notBefore notAfter selfSigned pinnedAt } } }`
	webdavCreateMut = `mutation($in: CreateDestinationInput!) { createDestination(input: $in) { ` + webdavDestFields + ` } }`
	webdavUpdateMut = `mutation($id: UUID!, $in: UpdateDestinationInput!) { updateDestination(id: $id, input: $in) { ` + webdavDestFields + ` } }`
	webdavTestMut   = `mutation($id: UUID!) { testDestination(id: $id) { ok probeRemains steps { name ok code message }
		webdav { trust trusted pinMatches server suggestedFlavor uploadMode overwriteProtection
			certificate { spkiSha256 fingerprintSha256 selfSigned } } } }`
	webdavPinMut   = `mutation($id: UUID!, $fp: String!) { pinWebdavCertificate(id: $id, fingerprint: $fp) { ` + webdavDestFields + ` } }`
	webdavUnpinMut = `mutation($id: UUID!) { clearWebdavCertificatePin(id: $id) { ` + webdavDestFields + ` } }`
)

type webdavCertModel struct {
	SpkiSha256        string
	FingerprintSha256 string
	SelfSigned        bool
}

type webdavDestModel struct {
	ID             string
	Type           string
	Ready          bool
	NotReadyReason *string
	Settings       *struct {
		Webdav *struct {
			URL               string `json:"url"`
			Flavor            string
			Username          string
			HasPassword       bool
			Trust             string
			PinnedCertificate *webdavCertModel
		}
	}
}

type webdavTestModel struct {
	OK    bool `json:"ok"`
	Steps []struct {
		Name    string
		OK      bool `json:"ok"`
		Code    string
		Message string
	}
	Webdav *struct {
		Trust               string
		Trusted             bool
		PinMatches          bool
		Server              *string
		SuggestedFlavor     string
		UploadMode          *string
		OverwriteProtection *string
		Certificate         *webdavCertModel
	}
}

// TestDestinationsWebDAV: create, the untrusted test showing the
// certificate, the pin flow (admin + step-up, CONFLICT for another key),
// a pinned test with the overwrite probe, a pin mismatch as a security
// event, clearing the pin, and D-140 on a URL change (Apache mod_dav).
func TestDestinationsWebDAV(t *testing.T) {
	webdavtest.Ensure(t, webdavtest.ApacheImage)
	srv, err := webdavtest.StartApache(context.Background())
	if err != nil {
		t.Fatalf("start apache: %v", err)
	}
	t.Cleanup(func() {
		if err := srv.Terminate(context.Background()); err != nil {
			t.Logf("terminate: %v", err)
		}
	})
	e := newDestEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	e.user("maint@example.com", sqlc.UserRoleMaintainer)
	adminC := e.mustLogin("admin@example.com")
	maintC := e.mustLogin("maint@example.com")
	e.freshStepUp(admin.ID)

	in := func(url string, password any) map[string]any {
		m := map[string]any{"url": url, "username": srv.User, "path": "bekup"}
		if password != nil {
			m["password"] = password
		}
		return m
	}
	var d webdavDestModel
	e.mustGQL(adminC, webdavCreateMut, map[string]any{"in": map[string]any{"name": "nas", "type": "WEBDAV", "webdav": in(srv.URL, srv.Password)}}).
		field(t, "createDestination", &d)
	w := d.Settings.Webdav
	if !d.Ready || d.Type != "WEBDAV" || w.Flavor != "GENERIC" || !w.HasPassword || w.Trust != "SYSTEM" || w.PinnedCertificate != nil {
		t.Fatalf("created: %+v %+v", d, w)
	}

	// Untrusted (self-signed): the test shows the certificate.
	var res webdavTestModel
	e.mustGQL(maintC, webdavTestMut, map[string]any{"id": d.ID}).field(t, "testDestination", &res)
	if res.OK || len(res.Steps) != 1 || res.Steps[0].Code != "network" || !strings.Contains(res.Steps[0].Message, "not trusted") ||
		res.Webdav == nil || res.Webdav.Trusted || res.Webdav.Certificate == nil || !res.Webdav.Certificate.SelfSigned {
		t.Fatalf("untrusted test: %+v", res)
	}
	cert, err := webdav.ParseCertificatePEM(srv.CertPEM)
	if err != nil {
		t.Fatal(err)
	}
	want := webdav.CertificateOf(cert)
	spki := res.Webdav.Certificate.SpkiSha256
	if !strings.EqualFold(spki, hexOf(want.SPKISHA256)) {
		t.Fatalf("shown fingerprint %s", spki)
	}

	// Maintainers cannot pin; another fingerprint is a CONFLICT.
	if r := e.gql(maintC, webdavPinMut, map[string]any{"id": d.ID, "fp": spki}); r.code() != "FORBIDDEN" {
		t.Fatalf("maintainer pin: %+v", r.Errors)
	}
	if r := e.gql(adminC, webdavPinMut, map[string]any{"id": d.ID, "fp": strings.Repeat("ab", 32)}); r.code() != "CONFLICT" {
		t.Fatalf("wrong fingerprint: %+v", r.Errors)
	}
	var pinned webdavDestModel
	e.mustGQL(adminC, webdavPinMut, map[string]any{"id": d.ID, "fp": spki}).field(t, "pinWebdavCertificate", &pinned)
	if p := pinned.Settings.Webdav; p.Trust != "PINNED" || p.PinnedCertificate == nil || p.PinnedCertificate.SpkiSha256 != spki {
		t.Fatalf("pinned: %+v", p)
	}
	if n := countAudit(t, e.auditor, audit.ActionDestinationTLSPinned); n != 1 {
		t.Fatalf("pin audited %d times", n)
	}

	// Pinned: the full test passes, with the overwrite probe.
	e.mustGQL(adminC, webdavTestMut, map[string]any{"id": d.ID}).field(t, "testDestination", &res)
	if !res.OK || len(res.Steps) != 5 || res.Steps[1].Name != "overwrite" || res.Webdav.Trust != "PINNED" || !res.Webdav.PinMatches ||
		res.Webdav.OverwriteProtection == nil || *res.Webdav.OverwriteProtection != "REFUSED" ||
		res.Webdav.UploadMode == nil || *res.Webdav.UploadMode != "SINGLE" || res.Webdav.Server != nil || res.Webdav.SuggestedFlavor != "GENERIC" {
		t.Fatalf("pinned test: %+v %+v", res, res.Webdav)
	}

	// Another key than the pinned one: a security event.
	otherPEM, _, err := webdavtest.SelfSigned()
	if err != nil {
		t.Fatal(err)
	}
	oc, err := webdav.ParseCertificatePEM(otherPEM)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), "UPDATE destinations SET tls_pin_sha256 = $2, tls_pin_certificate = $3 WHERE id = $1",
		d.ID, hexOf(webdav.CertificateOf(oc).SPKISHA256), string(otherPEM)); err != nil {
		t.Fatal(err)
	}
	e.mustGQL(adminC, webdavTestMut, map[string]any{"id": d.ID}).field(t, "testDestination", &res)
	if res.OK || res.Steps[0].Code != "tls_pin_mismatch" || res.Webdav.PinMatches {
		t.Fatalf("mismatch test: %+v", res)
	}
	if n := countAudit(t, e.auditor, audit.ActionDestinationTLSPinMismatch); n != 1 {
		t.Fatalf("pin mismatch audited %d times", n)
	}

	// Clearing the pin returns to the system roots (alerted).
	var cleared webdavDestModel
	e.mustGQL(adminC, webdavUnpinMut, map[string]any{"id": d.ID}).field(t, "clearWebdavCertificatePin", &cleared)
	if c := cleared.Settings.Webdav; c.Trust != "SYSTEM" || c.PinnedCertificate != nil {
		t.Fatalf("cleared: %+v", c)
	}
	if n := countAudit(t, e.auditor, audit.ActionDestinationTLSPinCleared); n != 1 {
		t.Fatalf("pin clear audited %d times", n)
	}

	// D-140: a new URL needs the password again and drops the pin.
	e.mustGQL(adminC, webdavPinMut, map[string]any{"id": d.ID, "fp": spki})
	r := e.gql(adminC, webdavUpdateMut, map[string]any{"id": d.ID, "in": map[string]any{"webdav": in(srv.Root+"/backups/other", nil)}})
	if r.code() != "VALIDATION" || !strings.Contains(fmt.Sprint(r.Errors), "webdav.password") {
		t.Fatalf("URL change without the password: %+v", r.Errors)
	}
	var moved webdavDestModel
	e.mustGQL(adminC, webdavUpdateMut, map[string]any{"id": d.ID, "in": map[string]any{"webdav": in(srv.Root+"/backups/other", srv.Password)}}).
		field(t, "updateDestination", &moved)
	if m := moved.Settings.Webdav; m.PinnedCertificate != nil || m.Trust != "SYSTEM" || !m.HasPassword {
		t.Fatalf("after a URL change: %+v", m)
	}
	// Plain http is refused outside dev mode.
	if r := e.gql(adminC, webdavCreateMut, map[string]any{"in": map[string]any{"name": "plain", "type": "WEBDAV",
		"webdav": in(strings.Replace(srv.URL, "https://", "http://", 1), srv.Password)}}); r.code() != "VALIDATION" {
		t.Fatalf("http URL: %+v", r.Errors)
	}
	e.assertNoSecrets(srv.Password)
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 2*len(b))
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&15])
	}
	return string(out)
}
