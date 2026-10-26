package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadSecretFile(t *testing.T) {
	dir := safeDir(t)
	tests := []struct {
		name    string
		content string
		mode    os.FileMode
		want    string
		wantErr string
	}{
		{"0400 trimmed", "  s3cr3t-value\n\n", 0o400, "s3cr3t-value", ""},
		{"0600", "abc", 0o600, "abc", ""},
		{"0640 group-readable ok", "abc", 0o640, "abc", ""},
		{"world-readable", "s3cr3t", 0o644, "", "world-readable"},
		{"group-writable", "s3cr3t", 0o660, "", "group- or world-writable"},
		{"empty", " \n", 0o600, "", "is empty"},
		{"too large", strings.Repeat("x", maxSecretBytes+1), 0o600, "", "larger than"},
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := writeFile(t, dir, fmt.Sprintf("secret%d", i), tc.content, tc.mode)
			got, err := ReadSecretFile(p)
			if tc.wantErr != "" {
				requireErrContains(t, err, tc.wantErr)
				if err != nil && strings.Contains(err.Error(), "s3cr3t") {
					t.Fatal("error leaks the secret")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
			Zero(got)
			if !bytes.Equal(got, make([]byte, len(got))) {
				t.Fatal("Zero did not clear the slice")
			}
		})
	}

	t.Run("missing", func(t *testing.T) {
		_, err := ReadSecretFile(filepath.Join(dir, "nope"))
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("want ErrNotExist, got %v", err)
		}
	})
	t.Run("empty path", func(t *testing.T) {
		if _, err := ReadSecretFile(""); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestMasterKeyFromFile(t *testing.T) {
	dir := safeDir(t)
	keyFile := writeFile(t, dir, "master.key", "key-material\n", 0o400)
	cfgFile := writeFile(t, dir, BekupFileName, validBekupTOML+"[security]\nmaster_key_file = \""+keyFile+"\"\n", 0o600)
	res, err := LoadBekup(opts([]string{cfgFile}, nil))
	if err != nil {
		t.Fatal(err)
	}
	k, err := res.Config.MasterKey()
	if err != nil || string(k) != "key-material" {
		t.Fatalf("MasterKey = %q, %v", k, err)
	}
}
