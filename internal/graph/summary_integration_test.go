//go:build integration

package graph_test

import (
	"testing"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

const (
	summaryFields  = `settings { frequency hour weekday monthDay timezone } custom defaults { frequency hour weekday monthDay timezone } nextSummaryAt lastSentAt lastOutcome emailVerified visibleJobs`
	mySummaryQuery = `{ mySummarySettings { ` + summaryFields + ` } }`
	setSummaryMut  = `mutation($in: SummarySettingsInput!) { setSummarySettings(input: $in) { ` + summaryFields + ` } }`
	previewMut     = `mutation { sendSummaryPreview { periodStart periodEnd emailVerified } }`
)

type summaryGQL struct {
	Settings struct {
		Frequency     string
		Hour, Weekday int
		MonthDay      int `json:"monthDay"`
		Timezone      string
	}
	Custom        bool
	Defaults      struct{ Frequency, Timezone string }
	NextSummaryAt *string `json:"nextSummaryAt"`
	EmailVerified bool    `json:"emailVerified"`
	VisibleJobs   int     `json:"visibleJobs"`
}

// TestSummarySettingsGraphQL: every role reads and sets its own summary
// settings (D-226); input is validated; the preview is single-root and
// rate limited; the summary event offers inbox and e-mail only.
func TestSummarySettingsGraphQL(t *testing.T) {
	e := newNotifEnv(t)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	e.own(e.job, owner.ID)
	e.user("lonely@example.com", sqlc.UserRoleUser)
	e.user("maint@example.com", sqlc.UserRoleMaintainer)
	oc, lc, mc := e.mustLogin("owner@example.com"), e.mustLogin("lonely@example.com"), e.mustLogin("maint@example.com")

	if r := e.gql("", mySummaryQuery, nil); r.code() != "UNAUTHENTICATED" {
		t.Fatalf("anonymous: %+v", r.Errors)
	}
	var v summaryGQL
	e.mustGQL(oc, mySummaryQuery, nil).field(t, "mySummarySettings", &v)
	if v.Custom || v.Settings.Frequency != "WEEKLY" || v.Settings.Hour != 8 || v.Settings.Weekday != 1 || v.Settings.MonthDay != 1 ||
		v.Settings.Timezone != "UTC" || v.Defaults.Frequency != "WEEKLY" || v.NextSummaryAt == nil || v.VisibleJobs != 1 {
		t.Fatalf("defaults: %+v", v)
	}
	in := map[string]any{"frequency": "EVERY_OTHER_DAY", "hour": 7, "weekday": 3, "monthDay": 15, "timezone": "Europe/Amsterdam"}
	e.mustGQL(oc, setSummaryMut, map[string]any{"in": in}).field(t, "setSummarySettings", &v)
	if !v.Custom || v.Settings.Frequency != "EVERY_OTHER_DAY" || v.Settings.Hour != 7 || v.Settings.Weekday != 3 ||
		v.Settings.MonthDay != 15 || v.Settings.Timezone != "Europe/Amsterdam" || v.NextSummaryAt == nil {
		t.Fatalf("saved: %+v", v)
	}
	// Another user's settings are untouched.
	var mv summaryGQL
	e.mustGQL(mc, mySummaryQuery, nil).field(t, "mySummarySettings", &mv)
	if mv.Custom || mv.VisibleJobs != 1 {
		t.Fatalf("maintainer: %+v", mv)
	}
	for _, bad := range []map[string]any{
		{"frequency": "WEEKLY", "hour": 24, "weekday": 1, "monthDay": 1, "timezone": "UTC"},
		{"frequency": "WEEKLY", "hour": 8, "weekday": 0, "monthDay": 1, "timezone": "UTC"},
		{"frequency": "MONTHLY", "hour": 8, "weekday": 1, "monthDay": 29, "timezone": "UTC"},
		{"frequency": "WEEKLY", "hour": 8, "weekday": 1, "monthDay": 1, "timezone": "Local"},
		{"frequency": "WEEKLY", "hour": 8, "weekday": 1, "monthDay": 1, "timezone": "Mars/Base"},
	} {
		if r := e.gql(oc, setSummaryMut, map[string]any{"in": bad}); r.code() != "VALIDATION" {
			t.Fatalf("%v: %+v", bad, r.Errors)
		}
	}

	// Preview: nothing to summarize without jobs; single-root; rate limited
	// (burst 3).
	if r := e.gql(lc, previewMut, nil); r.code() != "VALIDATION" {
		t.Fatalf("preview without jobs: %+v", r.Errors)
	}
	if r := e.gql(oc, `mutation { sendSummaryPreview { periodEnd } b: sendSummaryPreview { periodEnd } }`, nil); r.code() != "VALIDATION" {
		t.Fatalf("two previews in one request: %+v", r.Errors)
	}
	for i := range 3 {
		if r := e.gql(oc, previewMut, nil); len(r.Errors) > 0 {
			t.Fatalf("preview %d: %+v", i, r.Errors)
		}
	}
	if r := e.gql(oc, previewMut, nil); r.code() != "RATE_LIMITED" {
		t.Fatalf("fourth preview: %+v", r.Errors)
	}
	var n int
	if err := e.pool.QueryRow(t.Context(), `SELECT count(*) FROM notification_events WHERE type = 'summary.periodic' AND user_id = $1 AND (detail->>'preview')::boolean`, owner.ID).Scan(&n); err != nil || n != 3 {
		t.Fatalf("preview events = %d, %v", n, err)
	}

	// The matrix row has inbox and e-mail only; a webhook is refused.
	r := findPref(t, e.mustGQL(oc, prefsQuery, nil), "myNotificationPreferences", "summary.periodic")
	if len(r.Channels) != 2 || r.Channels[0].Channel != "INBOX" || r.Channels[1].Channel != "EMAIL" || !r.Channels[0].Enabled || !r.Channels[1].Enabled {
		t.Fatalf("summary row %+v", r)
	}
	if r := e.gql(oc, setPrefMut, map[string]any{"e": "summary.periodic", "c": "WEBHOOK", "on": true}); r.code() != "VALIDATION" {
		t.Fatalf("summary webhook: %+v", r.Errors)
	}
	e.mustGQL(oc, setPrefMut, map[string]any{"e": "summary.periodic", "c": "EMAIL", "on": false})
	var groups []struct {
		Group  string
		Events []struct {
			Type            string
			OfferedChannels []string `json:"offeredChannels"`
		}
	}
	e.mustGQL(oc, `{ notificationCatalog { group events { type offeredChannels } } }`, nil).field(t, "notificationCatalog", &groups)
	found := false
	for _, g := range groups {
		for _, ev := range g.Events {
			if ev.Type == "summary.periodic" {
				found = g.Group == "SUMMARY" && len(ev.OfferedChannels) == 2
			}
		}
	}
	if !found {
		t.Fatalf("catalog %+v", groups)
	}
}
