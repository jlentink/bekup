//go:build integration

package graph_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph"
	"github.com/jlentink/bekup/internal/graph/streams"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/notify/webhook"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/queue"
)

// nopQueue accepts River inserts (the fan-out's delivery jobs).
type nopQueue struct{}

func (nopQueue) InsertTx(context.Context, pgx.Tx, river.JobArgs, queue.InsertOptions) (queue.InsertResult, error) {
	return queue.InsertResult{JobID: 1}, nil
}

// hookReceiver is an httptest TLS webhook receiver.
type hookReceiver struct {
	srv    *httptest.Server
	mu     sync.Mutex
	status int
	reqs   []*http.Request
	bodies [][]byte
}

func (h *hookReceiver) last(t *testing.T) (*http.Request, []byte) {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.reqs) == 0 {
		t.Fatal("no webhook request")
	}
	return h.reqs[len(h.reqs)-1], h.bodies[len(h.bodies)-1]
}

type notifEnv struct {
	*runEnv
	hook   *hookReceiver
	notify *notify.Service
	mailer *fakeMailer
	job    uuid.UUID
}

func newNotifEnv(t *testing.T) *notifEnv {
	t.Helper()
	d := dbtest.Migrated(t)
	e := &authEnv{t: t, d: d, pool: d.AppPool(t)}
	e.auditor = audit.New(e.pool, audit.Options{Registry: logging.NewRegistry()})
	var err error
	e.hasher, err = auth.NewHasher(auth.HasherOptions{
		Params: auth.Argon2Params{MemoryKiB: 64, Iterations: 1, Parallelism: 1}, MaxConcurrent: 4, WeakForTests: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	bus := pubsub.NewBus()
	startBusListener(t, d, bus)
	sessions := auth.NewSessions(e.pool, auth.SessionOptions{Events: auth.PubSubSessionEvents{Publisher: &pubsub.Publisher{Bus: bus}}})
	svc, err := auth.NewService(auth.ServiceOptions{DB: e.pool, Hasher: e.hasher, Sessions: sessions, Auditor: e.auditor, Factors: auth.NewSecondFactors()})
	if err != nil {
		t.Fatal(err)
	}
	kr, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kr.Destroy)
	hook := &hookReceiver{status: http.StatusOK}
	hook.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		hook.mu.Lock()
		hook.reqs, hook.bodies = append(hook.reqs, r), append(hook.bodies, b)
		st := hook.status
		hook.mu.Unlock()
		w.WriteHeader(st)
	}))
	t.Cleanup(hook.srv.Close)
	// The netguard allowlist admits the loopback test receiver only.
	guard, err := netguard.New(netguard.Options{LoopbackAllowable: true, AllowCIDRs: []string{"127.0.0.1/32"}})
	if err != nil {
		t.Fatal(err)
	}
	tr, ok := hook.srv.Client().Transport.(*http.Transport)
	if !ok {
		t.Fatal("unexpected transport")
	}
	sender, err := webhook.NewSender(guard, webhook.SenderOptions{Version: "1.0.0", Timeout: 5 * time.Second, TLSConfig: tr.TLSClientConfig})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sender.CloseIdleConnections)
	logger := slog.New(slog.DiscardHandler)
	pol := authz.New(authz.Options{Ownership: jobs.Ownership{DB: e.pool}})
	mailer := &fakeMailer{}
	api, err := notify.NewAPI(notify.APIOptions{
		DB: e.pool, Keyring: kr, Auditor: e.auditor, Policy: pol, Guard: guard, PersonalGuard: guard,
		Webhooks: sender, PersonalWebhooks: sender, Mailer: mailer,
		Limiter: func(string) (bool, time.Duration) { return true, 0 }, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	core, err := notify.New(notify.Options{
		DB: e.pool, Policy: pol, Keyring: kr, Queue: func() notify.Inserter { return nopQueue{} },
		BaseURL: testBaseURL, Publisher: &pubsub.Publisher{Bus: bus}, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver := httpserver.SessionsResolver{Sessions: sessions}
	hub, err := streams.NewHub(streams.Options{Validator: resolver, Bus: bus, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	gql, err := graph.New(graph.Options{
		Logger: logger, MaxComplexity: 5000, MaxDepth: 12, MaxBodyBytes: 1 << 16,
		Access: graph.NewPolicyAccess(pol), Policy: pol, AuditLog: e.auditor, Auth: svc, Bus: bus,
		Streams:       &graph.StreamOptions{Hub: hub, AllowedOrigins: []string{testBaseURL}},
		Notifications: api,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := httpserver.New(httpserver.Options{
		BaseURL: testBaseURL, MaxBodyBytes: 1 << 20, Logger: logger,
		Ready:     func(context.Context) error { return nil },
		RateLimit: httpserver.RateLimitOptions{PerSecond: 1000, Burst: 1000, MaxClients: 100},
		GraphQL:   gql,
		Sessions:  &httpserver.SessionOptions{Resolver: resolver, Cookies: auth.CookiePolicyFor(testBaseURL)},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.h = srv.Handler()
	ts := httptest.NewServer(e.h)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := hub.Run(ctx); err != nil {
			t.Error(err)
		}
	}()
	t.Cleanup(func() { cancel(); <-done; ts.Close() })
	job, err := dbtest.InsertJob(t.Context(), e.pool, dbtest.JobRow{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	re := &runEnv{destEnv: &destEnv{authEnv: e}, events: bus, url: ts.URL + "/graphql"}
	return &notifEnv{runEnv: re, hook: hook, notify: core, mailer: mailer, job: job}
}

// shownOnce checks that every key appeared in exactly one response (the
// one that generated it) and never in the audit log.
func (e *notifEnv) shownOnce(keys ...string) {
	e.t.Helper()
	for _, k := range keys {
		n := 0
		for _, b := range e.bodies {
			if strings.Contains(b, k) {
				n++
			}
		}
		if n != 1 {
			e.t.Fatalf("a signing key appeared in %d responses", n)
		}
	}
	page, err := e.auditor.List(e.t.Context(), audit.Filter{}, 0, 200)
	if err != nil {
		e.t.Fatal(err)
	}
	for i := range page.Entries {
		for _, k := range keys {
			if strings.Contains(string(page.Entries[i].Details), k) {
				e.t.Fatal("the audit log contains a signing key")
			}
		}
	}
}

// emit emits ev and fans it out at once.
func (e *notifEnv) emit(ev notify.Event) {
	e.t.Helper()
	id, err := notify.Emit(e.t.Context(), sqlc.New(e.pool), ev)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.notify.FanOut(e.t.Context(), id); err != nil {
		e.t.Fatal(err)
	}
}

type sharedChannelGQL struct {
	ID         string
	Name       string
	Type       string
	Addresses  []string
	DisplayURL *string `json:"displayUrl"`
	State      struct {
		Enabled        bool
		DisabledReason *string
	}
	Attachments []struct {
		JobID  string
		Groups []string
		Events []string
	}
}

const (
	sharedFields     = `id name type addresses displayUrl state { enabled disabledReason disabledMessage } attachments { jobId jobName groups events }`
	createSharedMut  = `mutation($in: CreateSharedChannelInput!) { createSharedChannel(input: $in) { channel { ` + sharedFields + ` } signingKey } }`
	updateSharedMut  = `mutation($id: UUID!, $in: UpdateSharedChannelInput!) { updateSharedChannel(id: $id, input: $in) { ` + sharedFields + ` } }`
	rotateSharedMut  = `mutation($id: UUID!) { rotateSharedChannelKey(id: $id) { channel { id } signingKey } }`
	deleteSharedMut  = `mutation($id: UUID!) { deleteSharedChannel(id: $id) }`
	testSharedMut    = `mutation($id: UUID!) { testSharedChannel(id: $id) { ok code message statusCode } }`
	attachSharedMut  = `mutation($job: UUID!, $id: UUID!, $f: NotificationFilterInput) { attachSharedChannel(jobId: $job, channelId: $id, filter: $f) { ` + sharedFields + ` } }`
	detachSharedMut  = `mutation($job: UUID!, $id: UUID!) { detachSharedChannel(jobId: $job, channelId: $id) { ` + sharedFields + ` } }`
	disableSharedMut = `mutation($id: UUID!) { disableSharedChannel(id: $id) { ` + sharedFields + ` } }`
	enableSharedMut  = `mutation($id: UUID!) { enableSharedChannel(id: $id) { ` + sharedFields + ` } }`
	sharedListQuery  = `{ sharedChannels { ` + sharedFields + ` } }`
)

func TestSharedChannelsGraphQL(t *testing.T) {
	e := newNotifEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	e.user("maint@example.com", sqlc.UserRoleMaintainer)
	e.user("user@example.com", sqlc.UserRoleUser)
	ac, mc, uc := e.mustLogin("admin@example.com"), e.mustLogin("maint@example.com"), e.mustLogin("user@example.com")

	// The webhook URL carries a credential in its path.
	const credPath = "/services/T000/B000/XyZcredentialPart123"
	hookURL := e.hook.srv.URL + credPath
	in := map[string]any{"name": "Ops Slack", "type": "WEBHOOK", "url": hookURL}

	// Maintainers and users are denied everything.
	for _, c := range []string{mc, uc} {
		if r := e.gql(c, sharedListQuery, nil); r.code() != "FORBIDDEN" {
			t.Fatalf("list: %+v", r.Errors)
		}
		if r := e.gql(c, createSharedMut, map[string]any{"in": in}); r.code() != "FORBIDDEN" {
			t.Fatalf("create: %+v", r.Errors)
		}
		if r := e.gql(c, testSharedMut, map[string]any{"id": uuid.New()}); r.code() != "FORBIDDEN" {
			t.Fatalf("test: %+v", r.Errors)
		}
		if r := e.gql(c, attachSharedMut, map[string]any{"job": e.job, "id": uuid.New()}); r.code() != "FORBIDDEN" {
			t.Fatalf("attach: %+v", r.Errors)
		}
	}
	// Step-up required for changes that carry a URL or key.
	e.staleStepUp(admin.ID)
	if r := e.gql(ac, createSharedMut, map[string]any{"in": in}); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("stale: %+v", r.Errors)
	}
	e.freshStepUp(admin.ID)
	// URL rules: http and blocked hosts are refused without echoing them.
	for _, bad := range []string{"http://127.0.0.1/x", "https://10.1.2.3/hook", "https://user:pw@127.0.0.1/x"} {
		r := e.gql(ac, createSharedMut, map[string]any{"in": map[string]any{"name": "Bad", "type": "WEBHOOK", "url": bad}})
		if r.code() != "VALIDATION" || strings.Contains(r.Errors[0].Message, bad) {
			t.Fatalf("%s: %+v", bad, r.Errors)
		}
	}
	var created struct {
		Channel    sharedChannelGQL
		SigningKey *string
	}
	e.mustGQL(ac, createSharedMut, map[string]any{"in": in}).field(t, "createSharedChannel", &created)
	if created.SigningKey == nil || !strings.HasPrefix(*created.SigningKey, "bkwh_") {
		t.Fatalf("key %v", created.SigningKey)
	}
	key1 := *created.SigningKey
	if created.Channel.DisplayURL == nil || *created.Channel.DisplayURL != e.hook.srv.URL+"/…" || !created.Channel.State.Enabled {
		t.Fatalf("channel %+v", created.Channel)
	}
	id := created.Channel.ID
	// Duplicate names are refused.
	if r := e.gql(ac, createSharedMut, map[string]any{"in": in}); r.code() != "VALIDATION" {
		t.Fatalf("duplicate: %+v", r.Errors)
	}
	// E-mail lists: addresses validated.
	if r := e.gql(ac, createSharedMut, map[string]any{"in": map[string]any{"name": "DBA", "type": "EMAIL_LIST", "addresses": []string{"x\r\nBcc: y@example.com"}}}); r.code() != "VALIDATION" {
		t.Fatalf("bad address: %+v", r.Errors)
	}
	var list struct {
		Channel    sharedChannelGQL
		SigningKey *string
	}
	e.mustGQL(ac, createSharedMut, map[string]any{"in": map[string]any{"name": "DBA", "type": "EMAIL_LIST", "addresses": []string{"dba@example.com", "DBA@example.com", "ops@example.com"}}}).field(t, "createSharedChannel", &list)
	if list.SigningKey != nil || len(list.Channel.Addresses) != 2 || list.Channel.DisplayURL != nil {
		t.Fatalf("list %+v", list)
	}

	// The list never shows the key or the URL path.
	var channels []sharedChannelGQL
	resp := e.mustGQL(ac, sharedListQuery, nil)
	resp.field(t, "sharedChannels", &channels)
	if len(channels) != 2 || strings.Contains(string(resp.Data["sharedChannels"]), "credentialPart") {
		t.Fatalf("list %s", resp.Data["sharedChannels"])
	}

	// Rotation returns a new key once; the test send is signed with it.
	var rot struct{ SigningKey *string }
	e.mustGQL(ac, rotateSharedMut, map[string]any{"id": id}).field(t, "rotateSharedChannelKey", &rot)
	if rot.SigningKey == nil || *rot.SigningKey == key1 {
		t.Fatal("rotation did not return a new key")
	}
	if r := e.gql(ac, rotateSharedMut, map[string]any{"id": list.Channel.ID}); r.code() != "VALIDATION" {
		t.Fatalf("rotate e-mail list: %+v", r.Errors)
	}
	// Test sends are single-root.
	if r := e.gql(ac, `mutation($id: UUID!) { a: testSharedChannel(id: $id) { ok } b: testSharedChannel(id: $id) { ok } }`, map[string]any{"id": id}); r.code() != "VALIDATION" {
		t.Fatalf("two tests: %+v", r.Errors)
	}
	var res struct {
		Ok         bool
		Code       *string
		StatusCode *int
	}
	e.mustGQL(ac, testSharedMut, map[string]any{"id": id}).field(t, "testSharedChannel", &res)
	if !res.Ok {
		t.Fatalf("test %+v", res)
	}
	req, body := e.hook.last(t)
	if req.URL.Path != credPath || req.Header.Get("X-Bekup-Event") != webhook.EventTypeTest {
		t.Fatalf("request %s %v", req.URL.Path, req.Header)
	}
	if err := webhook.Verify([]byte(*rot.SigningKey), req.Header.Get("X-Bekup-Signature"), req.Header.Get("X-Bekup-Timestamp"),
		req.Header.Get("X-Bekup-Delivery"), req.Header.Get("X-Bekup-Event"), body, time.Now(), time.Minute); err != nil {
		t.Fatalf("signature: %v", err)
	}
	e.hook.mu.Lock()
	e.hook.status = http.StatusForbidden
	e.hook.mu.Unlock()
	e.mustGQL(ac, testSharedMut, map[string]any{"id": id}).field(t, "testSharedChannel", &res)
	if res.Ok || res.Code == nil || *res.Code != "http_status" || res.StatusCode == nil || *res.StatusCode != 403 {
		t.Fatalf("failed test %+v", res)
	}
	e.mustGQL(ac, testSharedMut, map[string]any{"id": list.Channel.ID}).field(t, "testSharedChannel", &res)
	if !res.Ok || len(e.mailer.last(t).To) != 2 {
		t.Fatalf("e-mail list test %+v", res)
	}

	// Attachment with a filter; security groups and unknown events refused.
	var ch sharedChannelGQL
	e.mustGQL(ac, attachSharedMut, map[string]any{"job": e.job, "id": id, "f": map[string]any{"groups": []string{"BACKUP"}, "events": []string{"restore.failed"}}}).field(t, "attachSharedChannel", &ch)
	if len(ch.Attachments) != 1 || ch.Attachments[0].JobID != e.job.String() || ch.Attachments[0].Groups[0] != "BACKUP" || ch.Attachments[0].Events[0] != "restore.failed" {
		t.Fatalf("attach %+v", ch)
	}
	for _, f := range []map[string]any{{"groups": []string{"SECURITY"}}, {"events": []string{"security.system"}}, {"events": []string{"nope.never"}}} {
		if r := e.gql(ac, attachSharedMut, map[string]any{"job": e.job, "id": id, "f": f}); r.code() != "VALIDATION" {
			t.Fatalf("filter %v: %+v", f, r.Errors)
		}
	}
	if r := e.gql(ac, attachSharedMut, map[string]any{"job": uuid.New(), "id": id}); r.code() != "NOT_FOUND" {
		t.Fatalf("unknown job: %+v", r.Errors)
	}
	e.mustGQL(ac, detachSharedMut, map[string]any{"job": e.job, "id": id}).field(t, "detachSharedChannel", &ch)
	if len(ch.Attachments) != 0 {
		t.Fatalf("detach %+v", ch)
	}
	// Disable / enable without step-up.
	e.staleStepUp(admin.ID)
	e.mustGQL(ac, disableSharedMut, map[string]any{"id": id}).field(t, "disableSharedChannel", &ch)
	if ch.State.Enabled || ch.State.DisabledReason == nil || *ch.State.DisabledReason != "manual" {
		t.Fatalf("disable %+v", ch)
	}
	e.mustGQL(ac, enableSharedMut, map[string]any{"id": id}).field(t, "enableSharedChannel", &ch)
	if !ch.State.Enabled || ch.State.DisabledReason != nil {
		t.Fatalf("enable %+v", ch)
	}
	// URL change and delete need step-up.
	if r := e.gql(ac, updateSharedMut, map[string]any{"id": id, "in": map[string]any{"url": hookURL + "2"}}); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("update stale: %+v", r.Errors)
	}
	e.freshStepUp(admin.ID)
	e.mustGQL(ac, updateSharedMut, map[string]any{"id": id, "in": map[string]any{"name": "Ops", "url": hookURL + "2"}}).field(t, "updateSharedChannel", &ch)
	if ch.Name != "Ops" {
		t.Fatalf("update %+v", ch)
	}
	e.mustGQL(ac, deleteSharedMut, map[string]any{"id": id})
	if r := e.gql(ac, deleteSharedMut, map[string]any{"id": id}); r.code() != "NOT_FOUND" {
		t.Fatalf("delete twice: %+v", r.Errors)
	}
	for action, want := range map[audit.Action]int{
		audit.ActionNotificationChannelCreated: 2, audit.ActionNotificationChannelKeyRotated: 1,
		audit.ActionNotificationChannelTested: 3, audit.ActionNotificationChannelAttached: 1,
		audit.ActionNotificationChannelDetached: 1, audit.ActionNotificationChannelDisabled: 1,
		audit.ActionNotificationChannelEnabled: 1, audit.ActionNotificationChannelUpdated: 1,
		audit.ActionNotificationChannelDeleted: 1,
	} {
		if n := countAudit(t, e.auditor, action); n != want {
			t.Errorf("%s: %d audit entries, want %d", action, n, want)
		}
	}
	e.assertNoSecrets("credentialPart")
	e.shownOnce(key1, *rot.SigningKey)
}

const (
	myChannelsQuery = `{ myNotificationChannels { email emailVerified webhooks { id label displayUrl state { enabled disabledReason disabledMessage permanentFailures } } maxWebhooks } }`
	addHookMut      = `mutation($url: String!, $label: String!) { addMyWebhook(url: $url, label: $label) { webhook { id label displayUrl state { enabled } } signingKey } }`
	rotateHookMut   = `mutation($id: UUID!) { rotateMyWebhookKey(id: $id) { webhook { id } signingKey } }`
	deleteHookMut   = `mutation($id: UUID!) { deleteMyWebhook(id: $id) }`
	testHookMut     = `mutation($id: UUID!) { testMyWebhook(id: $id) { ok code message statusCode } }`
	reenableHookMut = `mutation($id: UUID!) { reenableMyWebhook(id: $id) { id state { enabled disabledReason permanentFailures } } }`
)

func TestPersonalWebhooksGraphQL(t *testing.T) {
	e := newNotifEnv(t)
	user := e.user("pat@example.com", sqlc.UserRoleUser)
	other := e.user("other@example.com", sqlc.UserRoleUser)
	uc, oc := e.mustLogin("pat@example.com"), e.mustLogin("other@example.com")
	hookURL := e.hook.srv.URL + "/hook/personalPart42"

	var mine struct {
		Email         string
		EmailVerified bool
		Webhooks      []struct{ ID string }
		MaxWebhooks   int
	}
	e.mustGQL(uc, myChannelsQuery, nil).field(t, "myNotificationChannels", &mine)
	if mine.Email != "pat@example.com" || mine.EmailVerified || len(mine.Webhooks) != 0 || mine.MaxWebhooks != 5 {
		t.Fatalf("channels %+v", mine)
	}
	// Adding needs step-up; the key is returned once.
	e.staleStepUp(user.ID)
	if r := e.gql(uc, addHookMut, map[string]any{"url": hookURL, "label": "Phone"}); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("stale: %+v", r.Errors)
	}
	e.freshStepUp(user.ID)
	type added struct {
		Webhook struct {
			ID         string
			DisplayURL string `json:"displayUrl"`
		}
		SigningKey string
	}
	keys := make([]string, 0, 7)
	ids := make([]string, 0, 5)
	for i := range 5 {
		var a added
		e.mustGQL(uc, addHookMut, map[string]any{"url": hookURL, "label": "Hook " + string(rune('A'+i))}).field(t, "addMyWebhook", &a)
		if !strings.HasPrefix(a.SigningKey, "bkwh_") || a.Webhook.DisplayURL != e.hook.srv.URL+"/…" {
			t.Fatalf("added %+v", a)
		}
		keys, ids = append(keys, a.SigningKey), append(ids, a.Webhook.ID)
	}
	// At most 5.
	if r := e.gql(uc, addHookMut, map[string]any{"url": hookURL, "label": "Sixth"}); r.code() != "VALIDATION" {
		t.Fatalf("sixth: %+v", r.Errors)
	}
	resp := e.mustGQL(uc, myChannelsQuery, nil)
	resp.field(t, "myNotificationChannels", &mine)
	if len(mine.Webhooks) != 5 || strings.Contains(string(resp.Data["myNotificationChannels"]), "personalPart") {
		t.Fatalf("list %s", resp.Data["myNotificationChannels"])
	}
	// Another user cannot see, test, rotate or delete them.
	e.freshStepUp(other.ID)
	for _, q := range []string{testHookMut, rotateHookMut, deleteHookMut, reenableHookMut} {
		if r := e.gql(oc, q, map[string]any{"id": ids[0]}); r.code() != "NOT_FOUND" {
			t.Fatalf("other user %s: %+v", q, r.Errors)
		}
	}
	var otherList struct{ Webhooks []struct{ ID string } }
	e.mustGQL(oc, myChannelsQuery, nil).field(t, "myNotificationChannels", &otherList)
	if len(otherList.Webhooks) != 0 {
		t.Fatalf("other sees %+v", otherList)
	}

	// Test send (single-root) through the netguard-guarded sender.
	if r := e.gql(uc, `mutation($id: UUID!) { a: testMyWebhook(id: $id) { ok } b: reenableMyWebhook(id: $id) { id } }`, map[string]any{"id": ids[0]}); r.code() != "VALIDATION" {
		t.Fatalf("not single-root: %+v", r.Errors)
	}
	var res struct{ Ok bool }
	e.mustGQL(uc, testHookMut, map[string]any{"id": ids[0]}).field(t, "testMyWebhook", &res)
	req, body := e.hook.last(t)
	if !res.Ok || webhook.Verify([]byte(keys[0]), req.Header.Get("X-Bekup-Signature"), req.Header.Get("X-Bekup-Timestamp"),
		req.Header.Get("X-Bekup-Delivery"), req.Header.Get("X-Bekup-Event"), body, time.Now(), time.Minute) != nil {
		t.Fatalf("test %+v", res)
	}
	// A failing personal test is bucketed: code failed, no status (M5
	// review: no probing of services through test sends).
	e.hook.mu.Lock()
	e.hook.status = http.StatusTeapot
	e.hook.mu.Unlock()
	var failed struct {
		Ok         bool
		Code       *string
		StatusCode *int
	}
	e.mustGQL(uc, testHookMut, map[string]any{"id": ids[0]}).field(t, "testMyWebhook", &failed)
	if failed.Ok || failed.Code == nil || *failed.Code != "failed" || failed.StatusCode != nil {
		t.Fatalf("failed personal test %+v", failed)
	}
	e.hook.mu.Lock()
	e.hook.status = http.StatusOK
	e.hook.mu.Unlock()

	// Auto-disabled state shows the reason; re-enable resets it.
	if _, err := e.pool.Exec(t.Context(), `UPDATE user_channels SET enabled = false, disabled_at = now(),
		disabled_reason = 'permanent_failures', permanent_failures = 5, last_failure_code = 'http_status' WHERE id = $1`, ids[1]); err != nil {
		t.Fatal(err)
	}
	var st struct {
		Webhooks []struct {
			ID    string
			State struct {
				Enabled           bool
				DisabledReason    *string
				DisabledMessage   *string
				PermanentFailures int
			}
		}
	}
	e.mustGQL(uc, myChannelsQuery, nil).field(t, "myNotificationChannels", &st)
	if w := st.Webhooks[1]; w.State.Enabled || w.State.DisabledReason == nil || *w.State.DisabledReason != "permanent_failures" ||
		w.State.DisabledMessage == nil || w.State.PermanentFailures != 5 {
		t.Fatalf("disabled state %+v", w)
	}
	e.staleStepUp(user.ID)
	var re struct {
		ID    string
		State struct {
			Enabled           bool
			DisabledReason    *string
			PermanentFailures int
		}
	}
	e.mustGQL(uc, reenableHookMut, map[string]any{"id": ids[1]}).field(t, "reenableMyWebhook", &re)
	if !re.State.Enabled || re.State.DisabledReason != nil || re.State.PermanentFailures != 0 {
		t.Fatalf("re-enabled %+v", re)
	}
	// Rotation and deletion need step-up.
	if r := e.gql(uc, rotateHookMut, map[string]any{"id": ids[0]}); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("rotate stale: %+v", r.Errors)
	}
	e.freshStepUp(user.ID)
	var rot struct{ SigningKey string }
	e.mustGQL(uc, rotateHookMut, map[string]any{"id": ids[0]}).field(t, "rotateMyWebhookKey", &rot)
	if rot.SigningKey == keys[0] || !strings.HasPrefix(rot.SigningKey, "bkwh_") {
		t.Fatal("rotation")
	}
	e.mustGQL(uc, deleteHookMut, map[string]any{"id": ids[4]})
	var a added
	e.mustGQL(uc, addHookMut, map[string]any{"url": hookURL, "label": "Again"}).field(t, "addMyWebhook", &a)
	e.assertNoSecrets("personalPart")
	e.shownOnce(append(keys, rot.SigningKey, a.SigningKey)...)
}

const (
	catalogQuery = `{ notificationCatalog { group label events { type label group audience problem mandatory priority severity perJob defaultChannels } } }`
	prefsQuery   = `{ myNotificationPreferences { emailVerified activeWebhooks groups { group label events { event { type mandatory } channels { channel enabled source locked } jobOverrides { jobId jobName channel enabled } } } } }`
	setPrefMut   = `mutation($e: String!, $c: NotificationChannelType!, $on: Boolean!, $job: UUID) { setNotificationPreference(event: $e, channel: $c, enabled: $on, jobId: $job) { event { type } channels { channel enabled source locked } jobOverrides { jobId jobName channel enabled } } }`
	resetPrefMut = `mutation($job: UUID) { resetNotificationPreferences(jobId: $job) { groups { events { event { type } channels { channel enabled source } jobOverrides { jobId } } } } }`
)

type prefRow struct {
	Event struct {
		Type      string
		Mandatory bool
	}
	Channels []struct {
		Channel, Source string
		Enabled, Locked bool
	}
	JobOverrides []struct {
		JobID, JobName, Channel string
		Enabled                 bool
	}
}

func findPref(t *testing.T, resp gqlResp, field, event string) prefRow {
	t.Helper()
	var p struct {
		Groups []struct{ Events []prefRow }
	}
	resp.field(t, field, &p)
	for _, g := range p.Groups {
		for _, r := range g.Events {
			if r.Event.Type == event {
				return r
			}
		}
	}
	t.Fatalf("no row %s", event)
	return prefRow{}
}

func TestNotificationPreferencesGraphQL(t *testing.T) {
	e := newNotifEnv(t)
	e.user("admin@example.com", sqlc.UserRoleAdmin)
	e.user("maint@example.com", sqlc.UserRoleMaintainer)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	e.own(e.job, owner.ID)
	other, err := dbtest.InsertJob(t.Context(), e.pool, dbtest.JobRow{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ac, mc, oc := e.mustLogin("admin@example.com"), e.mustLogin("maint@example.com"), e.mustLogin("owner@example.com")

	// The catalog is filtered by role.
	types := func(c string) map[string]bool {
		var groups []struct {
			Group  string
			Events []struct{ Type string }
		}
		e.mustGQL(c, catalogQuery, nil).field(t, "notificationCatalog", &groups)
		out := map[string]bool{}
		for _, g := range groups {
			for _, ev := range g.Events {
				out[ev.Type] = true
			}
		}
		return out
	}
	at, mt, ot := types(ac), types(mc), types(oc)
	if !at["security.system"] || mt["security.system"] || !mt["connection.pin_changed"] || ot["connection.pin_changed"] ||
		ot["destination.test_failed"] || !ot["backup.failed"] || !ot["security.account"] || len(at) != len(notify.Catalog()) {
		t.Fatalf("catalog admin %v maint %v owner %v", at, mt, ot)
	}

	// Defaults: inbox on, e-mail for problems, webhook off.
	resp := e.mustGQL(oc, prefsQuery, nil)
	r := findPref(t, resp, "myNotificationPreferences", "backup.failed")
	if !r.Channels[0].Enabled || !r.Channels[1].Enabled || r.Channels[2].Enabled || r.Channels[0].Source != "DEFAULT" {
		t.Fatalf("defaults %+v", r)
	}
	sec := findPref(t, resp, "myNotificationPreferences", "security.account")
	if !sec.Channels[0].Locked || sec.Channels[0].Source != "MANDATORY" || !sec.Channels[1].Locked || sec.Channels[2].Locked {
		t.Fatalf("security row %+v", sec)
	}
	// Mandatory rows are locked.
	for _, ch := range []string{"INBOX", "EMAIL"} {
		if r := e.gql(oc, setPrefMut, map[string]any{"e": "security.account", "c": ch, "on": false}); r.code() != "VALIDATION" {
			t.Fatalf("mandatory %s: %+v", ch, r.Errors)
		}
	}
	// Webhook may be added to a security event.
	e.mustGQL(oc, setPrefMut, map[string]any{"e": "security.account", "c": "WEBHOOK", "on": true})
	// Events the role cannot receive are unknown.
	if r := e.gql(oc, setPrefMut, map[string]any{"e": "security.system", "c": "WEBHOOK", "on": true}); r.code() != "VALIDATION" {
		t.Fatalf("hidden event: %+v", r.Errors)
	}
	// Per-event override.
	var row prefRow
	e.mustGQL(oc, setPrefMut, map[string]any{"e": "backup.failed", "c": "EMAIL", "on": false}).field(t, "setNotificationPreference", &row)
	if row.Channels[1].Enabled || row.Channels[1].Source != "OVERRIDE" {
		t.Fatalf("override %+v", row)
	}
	// Per-job override for an own job; a job the owner cannot see is
	// NOT_FOUND; a non-job event refuses a job.
	e.mustGQL(oc, setPrefMut, map[string]any{"e": "backup.failed", "c": "EMAIL", "on": true, "job": e.job}).field(t, "setNotificationPreference", &row)
	if len(row.JobOverrides) != 1 || row.JobOverrides[0].JobID != e.job.String() || !row.JobOverrides[0].Enabled || row.Channels[1].Enabled {
		t.Fatalf("job override %+v", row)
	}
	if r := e.gql(oc, setPrefMut, map[string]any{"e": "backup.failed", "c": "EMAIL", "on": true, "job": other}); r.code() != "NOT_FOUND" {
		t.Fatalf("other job: %+v", r.Errors)
	}
	if r := e.gql(oc, setPrefMut, map[string]any{"e": "security.account", "c": "WEBHOOK", "on": true, "job": e.job}); r.code() != "VALIDATION" {
		t.Fatalf("job on non-job event: %+v", r.Errors)
	}
	// The override takes effect in the fan-out: e-mail for this job only.
	enabled, err := sqlc.New(e.pool).ListNotificationPrefs(t.Context(), sqlc.ListNotificationPrefsParams{UserIds: []uuid.UUID{owner.ID}, Event: "backup.failed", JobID: &e.job})
	if err != nil || len(enabled) != 2 {
		t.Fatalf("stored %v %v", enabled, err)
	}
	// Losing the job hides its override.
	if _, err := e.pool.Exec(t.Context(), `DELETE FROM job_owners WHERE job_id = $1`, e.job); err != nil {
		t.Fatal(err)
	}
	if r := findPref(t, e.mustGQL(oc, prefsQuery, nil), "myNotificationPreferences", "backup.failed"); len(r.JobOverrides) != 0 {
		t.Fatalf("override of a lost job shown %+v", r)
	}
	e.own(e.job, owner.ID)
	// Reset one job, then everything.
	resp = e.mustGQL(oc, resetPrefMut, map[string]any{"job": e.job})
	if r := findPref(t, resp, "resetNotificationPreferences", "backup.failed"); len(r.JobOverrides) != 0 || r.Channels[1].Source != "OVERRIDE" {
		t.Fatalf("job reset %+v", r)
	}
	resp = e.mustGQL(oc, resetPrefMut, nil)
	if r := findPref(t, resp, "resetNotificationPreferences", "backup.failed"); r.Channels[1].Source != "DEFAULT" || !r.Channels[1].Enabled {
		t.Fatalf("reset %+v", r)
	}
	if r := findPref(t, resp, "resetNotificationPreferences", "security.account"); r.Channels[2].Enabled {
		t.Fatalf("reset webhook %+v", r)
	}
}

const (
	inboxQuery  = `query($u: Boolean, $first: Int, $after: String) { notifications(unreadOnly: $u, first: $first, after: $after) { edges { cursor node { id type group severity title body link data createdAt readAt } } pageInfo { hasNextPage endCursor } } }`
	unreadQuery = `{ unreadNotificationCount }`
	markReadMut = `mutation($ids: [UUID!], $all: Boolean) { markNotificationsRead(ids: $ids, all: $all) { marked unreadCount } }`
	deleteNMut  = `mutation($id: UUID!) { deleteNotification(id: $id) }`
	receivedSub = `subscription { accountEvents { session { kind } notification { id type group title data } } }`
)

type inboxPage struct {
	Edges []struct {
		Cursor string
		Node   struct {
			ID, Type, Title string
			Group           *string
			ReadAt          *string
		}
	}
	PageInfo struct {
		HasNextPage bool
		EndCursor   *string
	}
}

func TestInboxGraphQL(t *testing.T) {
	e := newNotifEnv(t)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	e.user("other@example.com", sqlc.UserRoleUser)
	e.own(e.job, owner.ID)
	oc, xc := e.mustLogin("owner@example.com"), e.mustLogin("other@example.com")
	for range 5 {
		e.emit(notify.Event{Type: notify.BackupFailed, Subject: notify.Subject{JobID: e.job, RunID: uuid.New()}})
	}
	var n int
	e.mustGQL(oc, unreadQuery, nil).field(t, "unreadNotificationCount", &n)
	if n != 5 {
		t.Fatalf("unread %d", n)
	}
	e.mustGQL(xc, unreadQuery, nil).field(t, "unreadNotificationCount", &n)
	if n != 0 {
		t.Fatalf("other unread %d", n)
	}
	// Pagination.
	var p1, p2 inboxPage
	e.mustGQL(oc, inboxQuery, map[string]any{"first": 3}).field(t, "notifications", &p1)
	if len(p1.Edges) != 3 || !p1.PageInfo.HasNextPage || p1.Edges[0].Node.Type != "backup.failed" || p1.Edges[0].Node.Group == nil {
		t.Fatalf("page 1 %+v", p1)
	}
	e.mustGQL(oc, inboxQuery, map[string]any{"first": 3, "after": *p1.PageInfo.EndCursor}).field(t, "notifications", &p2)
	if len(p2.Edges) != 2 || p2.PageInfo.HasNextPage || p2.Edges[0].Node.ID == p1.Edges[2].Node.ID {
		t.Fatalf("page 2 %+v", p2)
	}
	if r := e.gql(oc, inboxQuery, map[string]any{"after": "garbage"}); r.code() != "VALIDATION" {
		t.Fatalf("bad cursor: %+v", r.Errors)
	}
	// Mark read: ids of another user change nothing.
	first := p1.Edges[0].Node.ID
	var mr struct{ Marked, UnreadCount int }
	e.mustGQL(xc, markReadMut, map[string]any{"ids": []string{first}}).field(t, "markNotificationsRead", &mr)
	if mr.Marked != 0 {
		t.Fatalf("other marked %+v", mr)
	}
	e.mustGQL(oc, markReadMut, map[string]any{"ids": []string{first}}).field(t, "markNotificationsRead", &mr)
	if mr.Marked != 1 || mr.UnreadCount != 4 {
		t.Fatalf("mark %+v", mr)
	}
	var unread inboxPage
	e.mustGQL(oc, inboxQuery, map[string]any{"u": true}).field(t, "notifications", &unread)
	if len(unread.Edges) != 4 {
		t.Fatalf("unread page %+v", unread)
	}
	if r := e.gql(oc, markReadMut, nil); r.code() != "VALIDATION" {
		t.Fatalf("no ids: %+v", r.Errors)
	}
	e.mustGQL(oc, markReadMut, map[string]any{"all": true}).field(t, "markNotificationsRead", &mr)
	if mr.Marked != 4 || mr.UnreadCount != 0 {
		t.Fatalf("mark all %+v", mr)
	}
	// Delete: own only.
	if r := e.gql(xc, deleteNMut, map[string]any{"id": first}); r.code() != "NOT_FOUND" {
		t.Fatalf("other delete: %+v", r.Errors)
	}
	e.mustGQL(oc, deleteNMut, map[string]any{"id": first})
	if r := e.gql(oc, deleteNMut, map[string]any{"id": first}); r.code() != "NOT_FOUND" {
		t.Fatalf("delete twice: %+v", r.Errors)
	}
}

func TestNotificationReceivedStream(t *testing.T) {
	e := newNotifEnv(t)
	e.user("admin@example.com", sqlc.UserRoleAdmin)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	e.user("other@example.com", sqlc.UserRoleUser)
	e.own(e.job, owner.ID)
	ac, oc, xc := e.mustLogin("admin@example.com"), e.mustLogin("owner@example.com"), e.mustLogin("other@example.com")

	ownerS := e.openStream(oc, receivedSub, nil)
	adminS := e.openStream(ac, receivedSub, nil)
	otherS := e.openStream(xc, receivedSub, nil)
	deadline := time.Now().Add(10 * time.Second)
	for e.events.Subscribers(pubsub.NotificationEvents.Name) < 3 {
		if time.Now().After(deadline) {
			t.Fatal("streams did not subscribe")
		}
		time.Sleep(10 * time.Millisecond)
	}

	e.emit(notify.Event{Type: notify.BackupFailed, Subject: notify.Subject{JobID: e.job}})
	ownRow := func(user uuid.UUID) string {
		var id string
		if err := e.pool.QueryRow(t.Context(), `SELECT id FROM notifications WHERE user_id = $1 ORDER BY created_at DESC LIMIT 1`, user).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	var got struct{ ID, Type, Title string }
	nextNotification(t, ownerS, &got)
	if got.ID != ownRow(owner.ID) || got.Type != "backup.failed" {
		t.Fatalf("owner got %+v", got)
	}
	var adminID uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `SELECT id FROM users WHERE email = 'admin@example.com'`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	nextNotification(t, adminS, &got)
	if got.ID != ownRow(adminID) {
		t.Fatalf("admin got %+v (not its own row)", got)
	}
	// The other user has no row and receives nothing (not the owner's or
	// the admin's notification).
	if ev, ok := nextEv(otherS, 500*time.Millisecond); ok {
		t.Fatalf("other user received %+v", ev)
	}
	// A forged announcement of the owner's row for the other user's id is
	// ignored (rows are loaded for the stream's own account).
	e.events.Publish(pubsub.NotificationEvents.Name, "inbox:"+ownRow(owner.ID)+":"+uuid.Nil.String())
	if ev, ok := nextEv(otherS, 300*time.Millisecond); ok {
		t.Fatalf("other user received %+v", ev)
	}

	// Logout ends the owner's stream.
	if r := e.gql(oc, logoutMutation, nil); len(r.Errors) > 0 {
		t.Fatal(r.Errors)
	}
	code := ""
	for {
		ev, ok := nextEv(ownerS, 10*time.Second)
		if !ok {
			t.Fatalf("stream did not end (code %q)", code)
		}
		if ev.event == "complete" {
			break
		}
		if strings.Contains(ev.data, `"UNAUTHENTICATED"`) {
			code = "UNAUTHENTICATED"
		}
	}
	if code != "UNAUTHENTICATED" {
		t.Fatalf("end code %q", code)
	}
	// Later events still reach the admin.
	e.emit(notify.Event{Type: notify.JobUpdated, Subject: notify.Subject{JobID: e.job}})
	for got.Type != "job.updated" {
		nextNotification(t, adminS, &got)
	}
}

// nextNotification reads accountEvents until the next notification
// event (session events of the same stream are skipped) and decodes it
// into v.
func nextNotification(t *testing.T, ch <-chan sseEv, v any) {
	t.Helper()
	for {
		var ev struct {
			Session      json.RawMessage `json:"session"`
			Notification json.RawMessage `json:"notification"`
		}
		nextData(t, ch, "accountEvents", &ev)
		hasSession := len(ev.Session) > 0 && string(ev.Session) != "null"
		hasNotification := len(ev.Notification) > 0 && string(ev.Notification) != "null"
		if hasSession == hasNotification {
			t.Fatalf("accountEvents event must set exactly one field: session=%s notification=%s", ev.Session, ev.Notification)
		}
		if !hasNotification {
			continue
		}
		if err := json.Unmarshal(ev.Notification, v); err != nil {
			t.Fatal(err)
		}
		return
	}
}
