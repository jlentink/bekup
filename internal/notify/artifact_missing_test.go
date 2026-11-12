package notify

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/i18n"
)

func TestArtifactMissingDetail(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("p", 190)
	for _, c := range []struct {
		name, object string
		newest       bool
		want         Detail
	}{
		{"layout key", "nightly/2026-11-09T020000Z/shop.dump.zst.age", true,
			Detail{"newest_complete": true, "file": "nightly/2026-11-09T020000Z/shop.dump.zst.age", "backup_time": "2026-11-09T02:00:00Z"}},
		{"long prefix keeps the file name", long + "/nightly/2026-11-09T020000Z/manifest.json", false,
			Detail{"newest_complete": false, "file": "manifest.json", "backup_time": "2026-11-09T02:00:00Z"}},
		{"no run directory", "manifest.json", false, Detail{"newest_complete": false, "file": "manifest.json"}},
		{"invalid key", "../x\n", false, Detail{"newest_complete": false}},
		{"empty", "", false, Detail{"newest_complete": false}},
	} {
		got := ArtifactMissingDetail(c.object, c.newest)
		if len(got) != len(c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
			continue
		}
		for k, v := range c.want {
			if got[k] != v {
				t.Errorf("%s: %s = %v, want %v", c.name, k, got[k], v)
			}
		}
		if _, err := ValidateDetail(got); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

// TestRenderArtifactMissing: the sentence names the file, the backup's
// time, the destination, the retention consequence and links to the run;
// an event without the detail keeps the old sentence.
func TestRenderArtifactMissing(t *testing.T) {
	s := testService(t)
	job, dest, run := uuid.New(), uuid.New(), uuid.New()
	m, _ := Lookup(ArtifactMissing)
	detail, err := json.Marshal(ArtifactMissingDetail("nightly/2026-11-09T020000Z/shop.dump.zst.age", true))
	if err != nil {
		t.Fatal(err)
	}
	n := names{job: "Watskebeurt", destination: "offsite"}
	ev := &sqlc.NotificationEvent{ID: uuid.New(), Type: string(ArtifactMissing), Severity: "error", JobID: &job,
		DestinationID: &dest, RunID: &run, Detail: detail, CreatedAt: time.Now()}

	got := s.render(ev, m, n, i18n.EN)
	want := `File "nightly/2026-11-09T020000Z/shop.dump.zst.age" of the backup of job "Watskebeurt" from 9 Nov 2026 02:00 UTC ` +
		`is missing from destination "offsite"; bekup did not delete it. It belonged to the newest complete backup there, ` +
		`so retention deletes nothing on that destination until a new backup completes. Check for deletions outside bekup ` +
		`(lifecycle rules, manual cleanup, other tools) and run a new backup.`
	if got.Body != want {
		t.Errorf("body\n %q\nwant\n %q", got.Body, want)
	}
	if got.Link != "https://bekup.example.com/app/runs/"+run.String() {
		t.Errorf("link %q", got.Link)
	}
	nl := s.render(ev, m, n, i18n.NL)
	for _, w := range []string{"Bestand", "9 nov 2026 02:00 UTC", `bestemming "offsite"`, "nieuwste volledige back-up"} {
		if !strings.Contains(nl.Body, w) {
			t.Errorf("Dutch body %q lacks %q", nl.Body, w)
		}
	}

	ev.Detail, ev.RunID = json.RawMessage(`{}`), nil
	got = s.render(ev, m, n, i18n.EN)
	if got.Body != `A backup file of job "Watskebeurt" is missing from its destination; bekup did not delete it.` {
		t.Errorf("legacy body %q", got.Body)
	}
	if got.Link != "https://bekup.example.com/app/jobs/"+job.String() {
		t.Errorf("legacy link %q", got.Link)
	}
}

// TestArtifactDeletedExternallyAlert: the admins' security alert carries
// the destination, run and file of the audit entry and renders them.
func TestArtifactDeletedExternallyAlert(t *testing.T) {
	job, dest, run, art := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	evs := securityFromAudit(audit.Event{Actor: audit.SystemActor(), Action: audit.ActionRetentionArtifactMissing,
		TargetType: "artifact", TargetID: art.String(), Details: audit.Details{
			"job_id": job.String(), "run_id": run.String(), "destination_id": dest.String(),
			"object": "nightly/2026-11-09T020000Z/shop.dump.zst.age", "newest_complete": false,
		}})
	if len(evs) != 1 {
		t.Fatalf("events %v", evs)
	}
	e := evs[0]
	if e.Subject.JobID != job || e.Subject.DestinationID != dest || e.Subject.RunID != run || e.Subject.ArtifactID != art {
		t.Errorf("subject %+v", e.Subject)
	}
	if _, err := ValidateDetail(e.Detail); err != nil {
		t.Fatal(err)
	}
	s := testService(t)
	m, _ := Lookup(SecuritySystem)
	detail, err := json.Marshal(e.Detail)
	if err != nil {
		t.Fatal(err)
	}
	got := s.render(&sqlc.NotificationEvent{ID: uuid.New(), Type: string(SecuritySystem), Severity: "error", JobID: &job,
		DestinationID: &dest, Detail: detail, CreatedAt: time.Now()}, m, names{job: "Watskebeurt", destination: "offsite"}, i18n.EN)
	for _, w := range []string{`"nightly/2026-11-09T020000Z/shop.dump.zst.age"`, `"Watskebeurt"`, "9 Nov 2026 02:00 UTC", `"offsite"`} {
		if !strings.Contains(got.Body, w) {
			t.Errorf("body %q lacks %q", got.Body, w)
		}
	}
	if strings.Contains(got.Body, "newest complete") {
		t.Errorf("body %q claims the newest backup", got.Body)
	}
}
