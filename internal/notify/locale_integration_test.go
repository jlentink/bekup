//go:build integration

package notify

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

// TestRecipientLanguage: inbox rows, e-mails and personal webhooks are
// rendered in each recipient's language (D-237); an account without a
// choice gets English.
func TestRecipientLanguage(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.user("admin", true)
	owner := e.user("user", true)
	e.exec(`UPDATE users SET locale = 'nl' WHERE id = $1`, owner)
	e.userWebhook(owner)
	job := e.job(owner)
	e.pref(owner, BackupFailed, &job, ChannelWebhook, true)

	id := e.emit(Event{Type: BackupFailed, Subject: Subject{JobID: job, RunID: uuid.New()}, Detail: Detail{"error_code": "dump_failed"}})
	e.fanOut(id)
	inbox := func(u uuid.UUID) (title, body string) {
		t.Helper()
		if err := e.pool.QueryRow(t.Context(), `SELECT title, body FROM notifications WHERE user_id = $1`, u).Scan(&title, &body); err != nil {
			t.Fatal(err)
		}
		return title, body
	}
	enTitle, enBody := inbox(admin)
	nlTitle, nlBody := inbox(owner)
	if enTitle == nlTitle || enBody == nlBody {
		t.Fatalf("same inbox text for both languages: %q / %q", enTitle, enBody)
	}
	if !strings.Contains(enBody, "failed") || strings.Contains(nlBody, "The backup") || !strings.Contains(nlBody, "mislukt") {
		t.Fatalf("inbox bodies: en %q, nl %q", enBody, nlBody)
	}

	e.deliverAll()
	var nlMail, enMail string
	for _, m := range e.mail.sent {
		switch m.To[0] {
		case owner.String()[:8] + "@example.com":
			nlMail = m.HTML
		case admin.String()[:8] + "@example.com":
			enMail = m.HTML
		}
	}
	if !strings.Contains(nlMail, `lang="nl"`) || !strings.Contains(nlMail, "mislukt") || !strings.Contains(nlMail, "Werkt de knop niet?") {
		t.Fatal("Dutch recipient's e-mail is not Dutch")
	}
	if !strings.Contains(enMail, `lang="en"`) || !strings.Contains(enMail, "failed") || strings.Contains(enMail, "mislukt") {
		t.Fatal("English recipient's e-mail is not English")
	}
	if len(e.bodies) != 1 || !strings.Contains(string(e.bodies[0]), "mislukt") {
		t.Fatalf("personal webhook payload is not Dutch: %s", e.bodies)
	}
}

// TestSharedChannelEnglish: a shared channel has no recipient language and
// stays English even when every member of the job is Dutch (D-237).
func TestSharedChannelEnglish(t *testing.T) {
	e := newEnv(t, nil)
	owner := e.user("user", false)
	e.exec(`UPDATE users SET locale = 'nl' WHERE id = $1`, owner)
	job := e.job(owner)
	ch := uuid.New()
	cfg, err := json.Marshal(EmailListConfig{Addresses: []string{"team@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	q := sqlc.New(e.pool)
	if _, err := q.InsertSharedChannel(t.Context(), sqlc.InsertSharedChannelParams{
		ID: ch, Name: "team", Type: sqlc.SharedChannelTypeEmailList, Config: cfg,
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.AttachSharedChannel(t.Context(), sqlc.AttachSharedChannelParams{JobID: job, ChannelID: ch, Groups: []string{"backup"}, Events: []string{}}); err != nil {
		t.Fatal(err)
	}
	e.fanOut(e.emit(Event{Type: BackupSucceeded, Subject: Subject{JobID: job}}))
	e.deliverAll()
	if e.mail.count() != 1 {
		t.Fatalf("shared mails %d", e.mail.count())
	}
	m := e.mail.sent[0]
	if !strings.Contains(m.HTML, `lang="en"`) || !strings.Contains(m.Text, "succeeded") || strings.Contains(m.Text, "geslaagd") {
		t.Fatalf("shared channel e-mail is not English: %q", m.Text)
	}
	var body string
	if err := e.pool.QueryRow(t.Context(), `SELECT body FROM notifications WHERE user_id = $1`, owner).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "geslaagd") {
		t.Fatalf("the Dutch owner's inbox row is not Dutch: %q", body)
	}
}
