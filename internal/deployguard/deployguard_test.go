package deployguard

import (
	"errors"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    Deployment
		wantErr bool
	}{
		{name: "unset", env: nil},
		{name: "empty", env: map[string]string{EnvSetupVersion: "", EnvImageMode: ""}},
		{name: "pinned", env: map[string]string{EnvSetupVersion: "0.0.3", EnvImageMode: "pinned"}, want: Deployment{SetupVersion: "0.0.3"}},
		{name: "following", env: map[string]string{EnvSetupVersion: "v1.2.3", EnvImageMode: "following"}, want: Deployment{SetupVersion: "1.2.3", ImageFollowing: true}},
		{name: "dev build", env: map[string]string{EnvSetupVersion: "0.0.0-dev+abc"}, want: Deployment{SetupVersion: "0.0.0-dev+abc"}},
		{name: "bad version", env: map[string]string{EnvSetupVersion: "latest"}, wantErr: true},
		{name: "bad version chars", env: map[string]string{EnvSetupVersion: "1.2.3;rm"}, wantErr: true},
		{name: "bad mode", env: map[string]string{EnvImageMode: "Following"}, wantErr: true},
		{name: "own-proxy join", env: map[string]string{EnvListenShape: "own-proxy-join"}, want: Deployment{OwnProxyJoin: true}},
		{name: "empty shape", env: map[string]string{EnvListenShape: ""}},
		{name: "bad shape", env: map[string]string{EnvListenShape: "anything"}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FromEnv(env(tc.env))
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("FromEnv = %+v, %v", got, err)
			}
		})
	}
}

func TestCheckVersion(t *testing.T) {
	tests := []struct {
		name, binary, setup string
		warn, refuse        bool
		msg                 string
	}{
		{name: "match", binary: "0.0.3", setup: "0.0.3"},
		{name: "match with v", binary: "v0.0.3", setup: "0.0.3"},
		{name: "unset warns", binary: "0.0.3", setup: "", warn: true, msg: EnvSetupVersion},
		{name: "upgrade without setup upgrade", binary: "0.0.4", setup: "0.0.3", refuse: true, msg: UpgradeCommand},
		{name: "downgrade", binary: "0.0.2", setup: "0.0.3", refuse: true, msg: "downgrade"},
		{name: "dev build against a release", binary: "0.0.0-dev", setup: "0.0.3", refuse: true, msg: "downgrade"},
		{name: "other build of the same version", binary: "0.0.3+b", setup: "0.0.3+a", refuse: true, msg: "another build"},
		{name: "prerelease to release", binary: "0.0.3", setup: "0.0.3-rc.1", refuse: true, msg: UpgradeCommand},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			warn, err := Deployment{SetupVersion: tc.setup}.CheckVersion(tc.binary)
			if (warn != "") != tc.warn || (err != nil) != tc.refuse {
				t.Fatalf("warn %q err %v", warn, err)
			}
			if err != nil && !errors.Is(err, ErrVersionMismatch) {
				t.Fatalf("not ErrVersionMismatch: %v", err)
			}
			if tc.msg != "" && !strings.Contains(warn+errString(err), tc.msg) {
				t.Fatalf("message %q / %v lacks %q", warn, err, tc.msg)
			}
		})
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// L1: a followed image without a setup version refuses outside dev mode.
func TestCheck(t *testing.T) {
	tests := []struct {
		name   string
		d      Deployment
		dev    bool
		warn   bool
		refuse error
	}{
		{name: "pinned match", d: Deployment{SetupVersion: "1.0.0"}},
		{name: "following match", d: Deployment{SetupVersion: "1.0.0", ImageFollowing: true}},
		{name: "pinned unversioned warns", d: Deployment{}, warn: true},
		{name: "pinned unversioned dev", d: Deployment{}, dev: true},
		{name: "following unversioned refuses", d: Deployment{ImageFollowing: true}, refuse: ErrFollowingUnversioned},
		{name: "following unversioned dev", d: Deployment{ImageFollowing: true}, dev: true},
		{name: "mismatch refuses in dev too", d: Deployment{SetupVersion: "0.9.0"}, dev: true, refuse: ErrVersionMismatch},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			warn, err := tc.d.Check("1.0.0", tc.dev)
			if (warn != "") != tc.warn {
				t.Fatalf("warn = %q", warn)
			}
			switch {
			case tc.refuse == nil && err != nil:
				t.Fatal(err)
			case tc.refuse != nil && !errors.Is(err, tc.refuse):
				t.Fatalf("err = %v, want %v", err, tc.refuse)
			}
			if errors.Is(err, ErrFollowingUnversioned) && !strings.Contains(err.Error(), UpgradeCommand) {
				t.Fatalf("message lacks the remedy: %v", err)
			}
		})
	}
}

func TestExpectedWildcardListen(t *testing.T) {
	join := Deployment{OwnProxyJoin: true}
	if !join.ExpectedWildcardListen("0.0.0.0:8080", "0.0.0.0:8080") {
		t.Error("the join shape expects exactly its listen address")
	}
	if join.ExpectedWildcardListen(":8080", "0.0.0.0:8080") || join.ExpectedWildcardListen("[::]:8080", "0.0.0.0:8080") {
		t.Error("another wildcard form is not the recorded shape")
	}
	if (Deployment{}).ExpectedWildcardListen("0.0.0.0:8080", "0.0.0.0:8080") {
		t.Error("without the shape the warning stays")
	}
}
