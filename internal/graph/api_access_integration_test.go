//go:build integration

package graph_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/apikey"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/externalapi"
)

const (
	apiTokenFields = `id name prefix createdAt expiresAt revokedAt allowedCidrs allowAnyAddress hiddenJobCount createdByMe
		createdBy { id email } jobs { id name deleted } lastUsedAt lastSeenAt lastSeenIp lastSeenRefused`
	createAPITokenMut = `mutation($in: CreateApiTokenInput!) { createApiToken(input: $in) { plaintext token { ` + apiTokenFields + ` } } }`
	listAPITokensQ    = `query($job: UUID) { apiTokens(jobId: $job) { ` + apiTokenFields + ` } }`
	revokeAPITokenMut = `mutation($id: UUID!) { revokeApiToken(id: $id) { ` + apiTokenFields + ` } }`
	updateAPIAddrMut  = `mutation($id: UUID!, $c: [String!]!, $any: Boolean!) {
		updateApiTokenAddresses(id: $id, allowedCidrs: $c, allowAnyAddress: $any) { ` + apiTokenFields + ` } }`
)

type apiTokenGQL struct {
	ID              string
	Name            string
	Prefix          string
	RevokedAt       *string
	AllowedCidrs    []string
	AllowAnyAddress bool
	HiddenJobCount  int
	CreatedByMe     bool
	CreatedBy       *struct{ ID, Email string }
	Jobs            []struct {
		ID, Name string
		Deleted  bool
	}
	LastSeenAt      *string
	LastSeenIP      *string
	LastSeenRefused bool
}

// startExternalAPI runs the real `bekup external-api` (as bekup_external)
// on a loopback port and returns its base URL.
func startExternalAPI(t *testing.T, d dbtest.DB) string {
	t.Helper()
	urlFile := filepath.Join(t.TempDir(), "url")
	if err := os.WriteFile(urlFile, []byte(d.ExternalURL), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultExternalAPI()
	cfg.Database.URLFile = urlFile
	cfg.Database.TLSCAFile = dbtest.CAFile()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.WithoutCancel(t.Context()))
	var wg sync.WaitGroup
	errc := make(chan error, 1)
	wg.Go(func() {
		errc <- externalapi.Run(ctx, externalapi.RunOptions{Config: &cfg, Logger: slog.New(slog.DiscardHandler), Listener: ln})
	})
	t.Cleanup(func() {
		stop()
		wg.Wait()
		if err := <-errc; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("external api: %v", err)
		}
	})
	base := "http://" + ln.Addr().String()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if s, _ := apiCall(t, http.MethodGet, base+"/readyz", ""); s == http.StatusOK {
			return base
		}
		if time.Now().After(deadline) {
			t.Fatal("the external API did not become ready")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func apiCall(t *testing.T, method, url, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b)
}

// TestAPITokensGraphQLEndToEnd: D-220 token management through GraphQL,
// then the created token against the real external API, then revocation.
func TestAPITokensGraphQLEndToEnd(t *testing.T) {
	e := newJobEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	maint := e.user("maint@example.com", sqlc.UserRoleMaintainer)
	e.user("other@example.com", sqlc.UserRoleUser)
	adminC := e.mustLogin("admin@example.com")
	ownerC := e.mustLogin("owner@example.com")
	maintC := e.mustLogin("maint@example.com")
	otherC := e.mustLogin("other@example.com")

	var a, b struct{ Job jobGQL }
	e.mustGQL(adminC, createJobMut, map[string]any{"in": e.createInput("Alpha")}).field(t, "createJob", &a)
	e.mustGQL(adminC, createJobMut, map[string]any{"in": e.createInput("Beta")}).field(t, "createJob", &b)
	e.freshStepUp(admin.ID)
	e.mustGQL(adminC, assignMut, map[string]any{"id": a.Job.ID, "u": []string{owner.ID.String()}})

	expires := time.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	input := func(jobs []string, cidrs []string, anyAddr bool) map[string]any {
		return map[string]any{"in": map[string]any{
			"name": "deploy pipeline", "jobIds": jobs, "expiresAt": expires, "allowedCidrs": cidrs, "allowAnyAddress": anyAddr,
		}}
	}
	loopback := input([]string{a.Job.ID}, []string{"127.0.0.0/8"}, false)

	// Maintainers never manage tokens; others only for their own jobs; a
	// stale step-up is asked for again; createApiToken is the only field
	// of its operation.
	e.freshStepUp(maint.ID)
	for _, q := range []struct {
		q    string
		vars map[string]any
	}{{createAPITokenMut, loopback}, {listAPITokensQ, nil}} {
		if r := e.gql(maintC, q.q, q.vars); r.code() != "FORBIDDEN" {
			t.Fatalf("maintainer: %+v", r.Errors)
		}
	}
	e.freshStepUp(owner.ID)
	if r := e.gql(ownerC, createAPITokenMut, input([]string{a.Job.ID, b.Job.ID}, []string{"127.0.0.0/8"}, false)); r.code() != "FORBIDDEN" {
		t.Fatalf("owner with a foreign job: %+v", r.Errors)
	}
	e.freshStepUp(owner.ID)
	r := e.gql(ownerC, createAPITokenMut, input([]string{a.Job.ID}, []string{}, false))
	if r.code() != "VALIDATION" || r.Errors[0].Extensions["field"] != "allowedCidrs" {
		t.Fatalf("no addresses: %+v", r.Errors)
	}
	if r := e.gql(ownerC, `mutation($in: CreateApiTokenInput!) { a: createApiToken(input: $in) { plaintext } b: createApiToken(input: $in) { plaintext } }`, loopback); r.code() != "VALIDATION" {
		t.Fatalf("a second root field: %+v", r.Errors)
	}
	e.staleStepUp(owner.ID)
	if r := e.gql(ownerC, createAPITokenMut, loopback); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("stale step-up: %+v", r.Errors)
	}

	// The owner creates a token for their job.
	e.freshStepUp(owner.ID)
	createdAt := len(e.bodies)
	var created struct {
		Plaintext string
		Token     apiTokenGQL
	}
	e.mustGQL(ownerC, createAPITokenMut, loopback).field(t, "createApiToken", &created)
	if _, err := apikey.Parse(created.Plaintext); err != nil {
		t.Fatalf("plaintext: %v", err)
	}
	tok := created.Token
	if tok.Name != "deploy pipeline" || !strings.HasPrefix(created.Plaintext, tok.Prefix) || len(tok.Jobs) != 1 || tok.Jobs[0].ID != a.Job.ID ||
		!tok.CreatedByMe || tok.CreatedBy == nil || tok.CreatedBy.Email != "owner@example.com" || len(tok.AllowedCidrs) != 1 {
		t.Fatalf("token %+v", tok)
	}

	// The token works against the external API from an allowed address.
	base := startExternalAPI(t, e.d)
	if s, body := apiCall(t, http.MethodGet, base+"/api/v1/self", created.Plaintext); s != http.StatusOK || !strings.Contains(body, a.Job.ID) ||
		!strings.Contains(body, `"clientIp":"127.0.0.1"`) {
		t.Fatalf("self: %d %s", s, body)
	}
	if s, body := apiCall(t, http.MethodPost, base+"/api/v1/jobs/"+b.Job.ID+"/runs", created.Plaintext); s != http.StatusNotFound {
		t.Fatalf("a job outside the scope: %d %s", s, body)
	}
	if s, body := apiCall(t, http.MethodPost, base+"/api/v1/jobs/"+a.Job.ID+"/runs", created.Plaintext); s != http.StatusAccepted {
		t.Fatalf("run request: %d %s", s, body)
	}

	// The listings: the owner's job panel and the admin page show where
	// the token was seen; other owners do not see it.
	var list []apiTokenGQL
	e.mustGQL(ownerC, listAPITokensQ, map[string]any{"job": a.Job.ID}).field(t, "apiTokens", &list)
	if len(list) != 1 || list[0].ID != tok.ID || list[0].LastSeenIP == nil || *list[0].LastSeenIP != "127.0.0.1" || list[0].LastSeenRefused {
		t.Fatalf("owner list %+v", list)
	}
	e.mustGQL(adminC, listAPITokensQ, nil).field(t, "apiTokens", &list)
	if len(list) != 1 || list[0].CreatedBy == nil || list[0].CreatedByMe {
		t.Fatalf("admin list %+v", list)
	}
	e.mustGQL(otherC, listAPITokensQ, nil).field(t, "apiTokens", &list)
	if len(list) != 0 {
		t.Fatalf("other owner list %+v", list)
	}
	if r := e.gql(otherC, listAPITokensQ, map[string]any{"job": a.Job.ID}); r.code() != "FORBIDDEN" {
		t.Fatalf("other owner, foreign job: %+v", r.Errors)
	}

	// Narrowing the addresses applies to the next call.
	e.freshStepUp(admin.ID)
	var upd apiTokenGQL
	e.mustGQL(adminC, updateAPIAddrMut, map[string]any{"id": tok.ID, "c": []string{"192.0.2.0/24"}, "any": false}).field(t, "updateApiTokenAddresses", &upd)
	if len(upd.AllowedCidrs) != 1 || upd.AllowedCidrs[0] != "192.0.2.0/24" {
		t.Fatalf("update %+v", upd)
	}
	if s, _ := apiCall(t, http.MethodGet, base+"/api/v1/self", created.Plaintext); s != http.StatusUnauthorized {
		t.Fatalf("self from a removed network: %d", s)
	}
	e.freshStepUp(owner.ID)
	e.mustGQL(ownerC, updateAPIAddrMut, map[string]any{"id": tok.ID, "c": []string{"127.0.0.1"}, "any": false})
	if s, _ := apiCall(t, http.MethodGet, base+"/api/v1/self", created.Plaintext); s != http.StatusOK {
		t.Fatalf("self after re-allowing: %d", s)
	}

	// Revocation: 401 from the next call on.
	if r := e.gql(otherC, revokeAPITokenMut, map[string]any{"id": tok.ID}); r.code() != "FORBIDDEN" && r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("other owner revoke: %+v", r.Errors)
	}
	e.freshStepUp(owner.ID)
	var rev apiTokenGQL
	e.mustGQL(ownerC, revokeAPITokenMut, map[string]any{"id": tok.ID}).field(t, "revokeApiToken", &rev)
	if rev.RevokedAt == nil {
		t.Fatalf("revoke %+v", rev)
	}
	if s, body := apiCall(t, http.MethodGet, base+"/api/v1/self", created.Plaintext); s != http.StatusUnauthorized {
		t.Fatalf("self after revocation: %d %s", s, body)
	}
	if r := e.gql(ownerC, revokeAPITokenMut, map[string]any{"id": tok.ID}); r.code() != "CONFLICT" {
		t.Fatalf("second revoke: %+v", r.Errors)
	}

	// The plaintext appeared in the create response only, never in the
	// audit log.
	for i, body := range e.bodies {
		if i != createdAt && strings.Contains(body, created.Plaintext) {
			t.Fatalf("response %d contains the token: %s", i, body)
		}
	}
	e.bodies = nil
	e.assertNoSecrets(created.Plaintext[len(apikey.Prefix)+apikey.IDChars+1:])

	// A token without expiry (D-229): an explicit choice; expiresAt with
	// neverExpires is refused, and /self reports expiresAt null.
	never := map[string]any{"in": map[string]any{
		"name": "forever", "jobIds": []string{a.Job.ID}, "neverExpires": true, "allowedCidrs": []string{"127.0.0.0/8"}, "allowAnyAddress": false,
	}}
	e.freshStepUp(owner.ID)
	both := map[string]any{"in": map[string]any{
		"name": "both", "jobIds": []string{a.Job.ID}, "expiresAt": expires, "neverExpires": true, "allowedCidrs": []string{"127.0.0.0/8"}, "allowAnyAddress": false,
	}}
	if r := e.gql(ownerC, createAPITokenMut, both); r.code() != "VALIDATION" || r.Errors[0].Extensions["field"] != "expiresAt" {
		t.Fatalf("expiry and never: %+v", r.Errors)
	}
	e.freshStepUp(owner.ID)
	var forever struct {
		Plaintext string
		Token     struct {
			ExpiresAt    *string
			NeverExpires bool
		}
	}
	e.mustGQL(ownerC, `mutation($in: CreateApiTokenInput!) { createApiToken(input: $in) { plaintext token { expiresAt neverExpires } } }`, never).
		field(t, "createApiToken", &forever)
	if forever.Token.ExpiresAt != nil || !forever.Token.NeverExpires {
		t.Fatalf("never-expiring token %+v", forever.Token)
	}
	if s, body := apiCall(t, http.MethodGet, base+"/api/v1/self", forever.Plaintext); s != http.StatusOK ||
		!strings.Contains(body, `"expiresAt":null`) || !strings.Contains(body, `"neverExpires":true`) {
		t.Fatalf("self of a never-expiring token: %d %s", s, body)
	}
}
