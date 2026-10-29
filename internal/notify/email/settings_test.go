package email

import (
	"errors"
	"strings"
	"testing"
)

func validInput() UpdateInput {
	return UpdateInput{
		Enabled: true, Host: "smtp.example.com", Port: 587, Mode: ModeSTARTTLS,
		Username: "bekup", Password: []byte("relay-password"), FromAddress: "bekup@example.com", FromName: "bekup",
	}
}

func TestValidate(t *testing.T) {
	in := validInput()
	c, err := validate(&in)
	if err != nil {
		t.Fatal(err)
	}
	if c.ConnectTimeoutSeconds != 10 || c.SendTimeoutSeconds != 60 {
		t.Fatalf("defaults: %+v", c)
	}
	cases := map[string]struct {
		mut   func(*UpdateInput)
		field string
	}{
		"no host":          {func(i *UpdateInput) { i.Host = "" }, "host"},
		"bad host":         {func(i *UpdateInput) { i.Host = "smtp example" }, "host"},
		"host injection":   {func(i *UpdateInput) { i.Host = "smtp.example.com\r\nRCPT" }, "host"},
		"port":             {func(i *UpdateInput) { i.Port = 0 }, "port"},
		"mode":             {func(i *UpdateInput) { i.Mode = "ssl" }, "mode"},
		"empty mode":       {func(i *UpdateInput) { i.Mode = "" }, "mode"},
		"legacy plaintext": {func(i *UpdateInput) { i.Mode = legacyModePlaintext }, "mode"},
		"mode case":        {func(i *UpdateInput) { i.Mode = "AUTO" }, "mode"},
		"none with CA":     {func(i *UpdateInput) { i.Mode = ModeNone; i.CABundlePEM = "x" }, "caBundlePem"},
		"username control": {func(i *UpdateInput) { i.Username = "a\nb" }, "username"},
		"from":             {func(i *UpdateInput) { i.FromAddress = "Eve <eve@example.com>" }, "fromAddress"},
		"from injection":   {func(i *UpdateInput) { i.FromAddress = "a@example.com\r\nBcc: x@example.com" }, "fromAddress"},
		"from name":        {func(i *UpdateInput) { i.FromName = "bekup\r\nBcc: x" }, "fromName"},
		"ca garbage":       {func(i *UpdateInput) { i.CABundlePEM = "not a pem" }, "caBundlePem"},
		"ca key":           {func(i *UpdateInput) { i.CABundlePEM = "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----" }, "caBundlePem"},
		"connect timeout":  {func(i *UpdateInput) { i.ConnectTimeoutSeconds = 61 }, "connectTimeoutSeconds"},
		"send timeout":     {func(i *UpdateInput) { i.SendTimeoutSeconds = 2 }, "sendTimeoutSeconds"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			in := validInput()
			tc.mut(&in)
			_, err := validate(&in)
			var fe *FieldError
			if !errors.As(err, &fe) || fe.Field != tc.field {
				t.Fatalf("got %v, want field %s", err, tc.field)
			}
		})
	}
	// Every mode is accepted with AUTH, in or outside dev mode (D-180).
	for _, m := range []Mode{ModeAuto, ModeSTARTTLS, ModeImplicitTLS, ModeNone} {
		in := validInput()
		in.Mode = m
		if _, err := validate(&in); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
	}
}

func TestModeParsing(t *testing.T) {
	for m, want := range map[Mode][2]bool{ // valid, may be unencrypted
		ModeAuto: {true, true}, ModeSTARTTLS: {true, false}, ModeImplicitTLS: {true, false}, ModeNone: {true, true},
		legacyModePlaintext: {false, false}, "": {false, false}, "tls": {false, false},
	} {
		if m.Valid() != want[0] || m.MayBeUnencrypted() != want[1] {
			t.Fatalf("%q: valid %v unencrypted %v", m, m.Valid(), m.MayBeUnencrypted())
		}
	}
	// Defaults of a fresh install: auto.
	if v := (*stored)(nil).view(); v.Mode != ModeAuto || !v.MayBeUnencrypted || v.Configured {
		t.Fatalf("defaults %+v", v)
	}
	st := &stored{cfg: Config{Mode: ModeNone}}
	if !st.view().MayBeUnencrypted {
		t.Fatal("none not flagged")
	}
}

func TestPasswordChange(t *testing.T) {
	cur := &stored{cfg: Config{Host: "smtp.example.com", Port: 587, Username: "bekup"}, sealed: []byte{1}}
	cfgSame := Config{Host: "smtp.example.com", Port: 587, Username: "bekup"}
	withMode := func(m Mode, ca string) Config {
		return Config{Host: "smtp.example.com", Port: 587, Username: "bekup", Mode: m, CABundlePEM: ca}
	}
	tlsCur := &stored{cfg: withMode(ModeSTARTTLS, ""), sealed: []byte{1}}
	implicitCur := &stored{cfg: withMode(ModeImplicitTLS, ""), sealed: []byte{1}}
	autoCur := &stored{cfg: withMode(ModeAuto, ""), sealed: []byte{1}}
	caCur := &stored{cfg: withMode(ModeSTARTTLS, "PEM\n"), sealed: []byte{1}}
	cases := []struct {
		name      string
		cur       *stored
		cfg       Config
		in        UpdateInput
		want      string
		wantField bool
	}{
		{"keep", cur, cfgSame, UpdateInput{}, "", false},
		{"set", cur, cfgSame, UpdateInput{Password: []byte("x")}, "set", false},
		{"clear needs no username", cur, cfgSame, UpdateInput{ClearPassword: true}, "", true},
		{"clear without username", cur, Config{Host: "smtp.example.com", Port: 587}, UpdateInput{ClearPassword: true}, "cleared", false},
		{"both", cur, cfgSame, UpdateInput{ClearPassword: true, Password: []byte("x")}, "", true},
		{"empty", cur, cfgSame, UpdateInput{Password: []byte{}}, "", true},
		{"newline", cur, cfgSame, UpdateInput{Password: []byte("a\nb")}, "", true},
		{"host changed keeps", cur, Config{Host: "other.example.com", Port: 587, Username: "bekup"}, UpdateInput{}, "", true},
		{"port changed keeps", cur, Config{Host: "smtp.example.com", Port: 465, Username: "bekup"}, UpdateInput{}, "", true},
		{"user changed keeps", cur, Config{Host: "smtp.example.com", Port: 587, Username: "other"}, UpdateInput{}, "", true},
		{"host changed reentered", cur, Config{Host: "other.example.com", Port: 587, Username: "bekup"}, UpdateInput{Password: []byte("x")}, "set", false},
		{"username without password", nil, cfgSame, UpdateInput{}, "", true},
		{"password without username", nil, Config{Host: "h"}, UpdateInput{Password: []byte("x")}, "", true},
		{"stored without username", cur, Config{Host: "smtp.example.com", Port: 587}, UpdateInput{}, "", true},
		// M5 review L3: weakening the transport or changing the CA bundle
		// needs the password again.
		{"starttls to auto keeps", tlsCur, withMode(ModeAuto, ""), UpdateInput{}, "", true},
		{"starttls to none keeps", tlsCur, withMode(ModeNone, ""), UpdateInput{}, "", true},
		{"implicit to auto keeps", implicitCur, withMode(ModeAuto, ""), UpdateInput{}, "", true},
		{"starttls to auto reentered", tlsCur, withMode(ModeAuto, ""), UpdateInput{Password: []byte("x")}, "set", false},
		{"starttls to implicit keeps", tlsCur, withMode(ModeImplicitTLS, ""), UpdateInput{}, "", false},
		{"auto to none keeps", autoCur, withMode(ModeNone, ""), UpdateInput{}, "", false},
		{"auto to starttls keeps", autoCur, withMode(ModeSTARTTLS, ""), UpdateInput{}, "", false},
		{"ca added keeps", tlsCur, withMode(ModeSTARTTLS, "PEM\n"), UpdateInput{}, "", true},
		{"ca changed reentered", caCur, withMode(ModeSTARTTLS, "OTHER\n"), UpdateInput{Password: []byte("x")}, "set", false},
		{"ca removed keeps", caCur, withMode(ModeSTARTTLS, ""), UpdateInput{}, "", true},
		{"ca same keeps", caCur, withMode(ModeSTARTTLS, "PEM\n"), UpdateInput{}, "", false},
		{"weakened and cleared", tlsCur, Config{Host: "smtp.example.com", Port: 587, Mode: ModeAuto}, UpdateInput{ClearPassword: true}, "cleared", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			has := tc.cur != nil && len(tc.cur.sealed) > 0
			got, err := passwordChange(tc.cur, &tc.cfg, &tc.in, has)
			var fe *FieldError
			if tc.wantField != errors.As(err, &fe) {
				t.Fatalf("err = %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDiffConfigHidesCABundle(t *testing.T) {
	cur := &stored{cfg: Config{Host: "a", CABundlePEM: "-----BEGIN CERTIFICATE-----"}}
	next := &Config{Host: "b", CABundlePEM: "other"}
	changed, values := diffConfig(cur, next)
	if strings.Join(changed, ",") != "ca_bundle_pem,host" {
		t.Fatalf("changed %v", changed)
	}
	if v, ok := values["ca_bundle_pem"].(map[string]any); !ok || v["old"] != true || v["new"] != true {
		t.Fatalf("ca bundle values %v", v)
	}
}
