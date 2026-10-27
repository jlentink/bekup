package resolvers

import (
	"testing"

	"github.com/jlentink/bekup/internal/graph/model"
)

func TestServerInfo(t *testing.T) {
	want := model.ServerInfo{Version: "1.2.3", InsecureTransport: true, DevMode: false}
	r := &Resolver{ServerInfo: want}
	got, err := r.Query().ServerInfo(t.Context())
	if err != nil || got == nil || *got != want {
		t.Fatalf("ServerInfo() = %+v, %v", got, err)
	}
	// The resolver returns a copy; callers cannot change the shared value.
	got.Version = "x"
	if r.ServerInfo.Version != "1.2.3" {
		t.Fatal("shared ServerInfo modified")
	}
}
