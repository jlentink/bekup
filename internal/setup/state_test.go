package setup

import (
	"strings"
	"testing"
)

func validState() *State {
	return &State{
		Schema: StateSchema, ManifestVersion: 1, BekupVersion: "1.2.3",
		Image: "ghcr.io/jlentink/bekup:1.2.3@" + testDigest, ComposeMode: ComposeGenerated, Variant: VariantFull,
		Proxy: ProxyTraefik, TLSMode: TLSACME,
		KeysDir: "secrets", Ownership: OwnershipChown,
		Assets:       map[string]string{ComposeFile: strings.Repeat("a", 64), "deploy/nginx/nginx.conf": strings.Repeat("b", 64)},
		SecretGroups: []string{groupCerts, groupDatabase, groupMasterKey},
	}
}

func TestParseState(t *testing.T) {
	data, err := validState().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseState(data); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*State){
		"schema":      func(s *State) { s.Schema = 2 },
		"version":     func(s *State) { s.BekupVersion = "1.2" },
		"image":       func(s *State) { s.Image = "bekup:dev" },
		"proxy":       func(s *State) { s.Proxy = "x" },
		"keys":        func(s *State) { s.KeysDir = "../x" },
		"ownership":   func(s *State) { s.Ownership = OwnershipAuto },
		"asset path":  func(s *State) { s.Assets["../../etc/passwd"] = strings.Repeat("a", 64) },
		"asset dev":   func(s *State) { s.Assets["deploy/dev/x"] = strings.Repeat("a", 64) },
		"asset hash":  func(s *State) { s.Assets[ComposeFile] = "zz" },
		"no compose":  func(s *State) { delete(s.Assets, ComposeFile) },
		"group":       func(s *State) { s.SecretGroups = []string{"x"} },
		"tls":         func(s *State) { s.TLSMode = "x" },
		"no manifest": func(s *State) { s.ManifestVersion = 0 },
		"variant":     func(s *State) { s.Variant = "x" },
		"no variant":  func(s *State) { s.Variant = "" },
		"mode":        func(s *State) { s.ComposeMode = "x" },
		"operator full": func(s *State) {
			s.ComposeMode, s.ComposeSHA256 = ComposeOperator, strings.Repeat("c", 64)
			delete(s.Assets, ComposeFile)
		},
		"operator without hash": func(s *State) {
			s.Variant, s.ComposeMode = VariantTraefik, ComposeOperator
			delete(s.Assets, ComposeFile)
		},
		"operator with compose asset": func(s *State) {
			s.Variant, s.ComposeMode, s.ComposeSHA256 = VariantTraefik, ComposeOperator, strings.Repeat("c", 64)
		},
		"generated with hash":   func(s *State) { s.ComposeSHA256 = strings.Repeat("c", 64) },
		"proxy for variant":     func(s *State) { s.Variant = VariantNPM },
		"external without link": func(s *State) { s.Variant, s.Proxy, s.TLSMode = VariantNPM, ProxyExternal, "" },
		"external bad link": func(s *State) {
			s.Variant, s.Proxy, s.TLSMode = VariantNPM, ProxyExternal, ""
			s.ProxyNetwork, s.ProxySubnet, s.ProxyIP, s.ServeIP = "bekup-proxy", "10.213.253.0/28", "10.213.253.5", "10.213.253.99"
		},
		"link on full": func(s *State) { s.ProxyNetwork = "bekup-proxy" },
	} {
		s := validState()
		mut(s)
		data, err := s.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseState(data); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ext := validState()
	ext.Variant, ext.Proxy, ext.TLSMode, ext.ComposeMode, ext.ComposeSHA256 = VariantNPM, ProxyExternal, "", ComposeOperator, strings.Repeat("c", 64)
	ext.ProxyNetwork, ext.ProxySubnet, ext.ProxyIP, ext.ServeIP = "bekup-proxy", "10.213.253.0/28", "10.213.253.5", "10.213.253.10"
	delete(ext.Assets, ComposeFile)
	if data, err = ext.Marshal(); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseState(data); err != nil {
		t.Fatalf("external operator state: %v", err)
	}
	if _, err := ParseState([]byte(`{"schema":1,"extra":1}`)); err == nil {
		t.Error("unknown field accepted")
	}
	if data, err = validState().Marshal(); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseState(append(data, []byte(`{}`)...)); err == nil {
		t.Error("trailing data accepted")
	}
}

func FuzzParseState(f *testing.F) {
	data, err := validState().Marshal()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(data)
	f.Add([]byte(`{"schema":1}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		s, err := ParseState(b)
		if err != nil {
			return
		}
		for p := range s.Assets {
			if !isAssetPath(p) || strings.Contains(p, "..") {
				t.Fatalf("accepted asset path %q", p)
			}
		}
		if ValidateKeysDir(s.KeysDir) != nil {
			t.Fatalf("accepted keys dir %q", s.KeysDir)
		}
	})
}

func TestVersionChecks(t *testing.T) {
	for _, tc := range []struct {
		tag, version string
		ok           bool
	}{
		{"", "1.2.3", false}, // tagless: nothing to compare (--allow-version-mismatch)
		{"1.2.3", "1.2.3", true},
		{"v1.2.3", "1.2.3", true},
		{"1.2.4", "1.2.3", false},
		{"latest", "1.2.3", false},
	} {
		img := mustRef(t, "bekup@"+testDigest)
		img.Tag = tc.tag
		if err := CheckVersionTag(img, tc.version); (err == nil) != tc.ok {
			t.Errorf("tag %q version %s: %v", tc.tag, tc.version, err)
		}
	}
	for _, tc := range []struct {
		prev, next string
		down       bool
	}{
		{"1.2.3", "1.2.4", false}, {"1.2.3", "1.2.3", false}, {"1.3.0", "1.2.9", true},
		{"1.0.0", "1.0.0-rc.1", true}, {"garbage", "1.0.0", false},
	} {
		if got := IsDowngrade(tc.prev, tc.next); got != tc.down {
			t.Errorf("IsDowngrade(%s, %s) = %v", tc.prev, tc.next, got)
		}
	}
}

func TestLineDiff(t *testing.T) {
	d := LineDiff("a\nb\nc\n", "a\nB\nc\n", 10)
	if d != "  + B\n  - b\n" && d != "  - b\n  + B\n" {
		t.Fatalf("%q", d)
	}
	if LineDiff("x", "x", 10) != "" {
		t.Fatal("diff of equal input")
	}
	if !strings.Contains(LineDiff(strings.Repeat("a\n", 50), strings.Repeat("b\n", 50), 5), "more changed line") {
		t.Fatal("no truncation")
	}
}
