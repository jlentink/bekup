package notify

import (
	"slices"
	"strings"
	"testing"
)

func TestInboxFilterTypes(t *testing.T) {
	backup, security := GroupBackup, GroupSecurity
	bogus := Group("nope")
	many := make([]string, MaxInboxFilterEvents+1)
	for i := range many {
		many[i] = string(BackupFailed)
	}
	tests := []struct {
		name    string
		f       InboxFilter
		want    []string // nil: no type filter
		wantErr string
	}{
		{name: "none", f: InboxFilter{UnreadOnly: true}},
		{name: "events", f: InboxFilter{Events: []string{"backup.failed", "backup.failed", NoticeWebhookDisabled}},
			want: []string{"backup.failed", NoticeWebhookDisabled}},
		{name: "group and event", f: InboxFilter{Group: &backup, Events: []string{"backup.failed", "restore.failed"}},
			want: []string{"backup.failed"}},
		{name: "disjoint", f: InboxFilter{Group: &security, Events: []string{"backup.failed"}}, want: []string{}},
		{name: "unknown event", f: InboxFilter{Events: []string{"backup.failed", "x.y"}}, wantErr: "filter.events.1"},
		{name: "unknown group", f: InboxFilter{Group: &bogus}, wantErr: "filter.group"},
		{name: "too many", f: InboxFilter{Events: many}, wantErr: "filter.events"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.f.types()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %s", err, tt.wantErr)
				}
				return
			}
			if err != nil || (got == nil) != (tt.want == nil) || !slices.Equal(got, tt.want) {
				t.Fatalf("types = %#v, %v; want %#v", got, err, tt.want)
			}
		})
	}
	// A group alone expands to exactly its catalog events.
	got, err := InboxFilter{Group: &security}.types()
	if err != nil || len(got) == 0 {
		t.Fatalf("security group: %v %v", got, err)
	}
	for _, ty := range got {
		if m, _ := Lookup(EventType(ty)); m.Group != GroupSecurity {
			t.Fatalf("%s is not a security event", ty)
		}
	}
}
