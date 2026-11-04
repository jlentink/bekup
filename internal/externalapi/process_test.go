package externalapi

import (
	"testing"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/deployguard"
)

// TestJoinMode (D-220 amendment 4): the listener filter of the own-proxy
// join shape applies only with BEKUP_LISTEN_SHAPE and exactly
// config.ExternalAPIJoinListen.
func TestJoinMode(t *testing.T) {
	t.Parallel()
	join := deployguard.Deployment{OwnProxyJoin: true}
	for _, tc := range []struct {
		listen string
		dep    deployguard.Deployment
		want   bool
	}{
		{config.ExternalAPIJoinListen, join, true},
		{config.ExternalAPIJoinListen, deployguard.Deployment{}, false},
		{"10.213.250.11:8081", join, false},
		{":8081", join, false},
		{config.OwnProxyJoinListen, join, false},
	} {
		c := config.DefaultExternalAPI()
		c.Listen.Address = tc.listen
		if got := JoinMode(&c, tc.dep); got != tc.want {
			t.Errorf("%s %+v: %v, want %v", tc.listen, tc.dep, got, tc.want)
		}
	}
}
