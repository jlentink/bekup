package restore

import (
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/connections"
)

func TestPrivilegeWarnings(t *testing.T) {
	if ws := privilegeWarnings(connections.Privileges{Status: connections.PrivilegeNotPrivileged}); len(ws) != 0 {
		t.Fatalf("not privileged: %+v", ws)
	}
	ws := privilegeWarnings(connections.Privileges{Status: connections.PrivilegePrivileged, Reasons: []string{"superuser", "global:FILE"}})
	if len(ws) != 1 || ws[0].Code != WarnPrivilegedTarget || !strings.Contains(ws[0].Message, "superuser, global:FILE") ||
		!strings.Contains(ws[0].Message, "Only admins") {
		t.Fatalf("privileged: %+v", ws)
	}
	ws = privilegeWarnings(connections.Privileges{Status: connections.PrivilegeUnknown, Reasons: []string{"roles_granted"}})
	if len(ws) != 1 || ws[0].Code != WarnPrivilegedTarget || !strings.Contains(ws[0].Message, "could not determine") {
		t.Fatalf("unknown: %+v", ws)
	}
}

func TestAcknowledged(t *testing.T) {
	if acknowledged(true) != nil {
		t.Fatal("acknowledged")
	}
	if err := acknowledged(false); err == nil || !strings.Contains(err.Error(), "acknowledgePrivilegedTarget") {
		t.Fatalf("missing acknowledgement: %v", err)
	}
}
