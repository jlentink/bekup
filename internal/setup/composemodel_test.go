package setup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
)

// TestCheckAliases: alias cycles, alias floods and alias bombs are refused
// before compose-go resolves the file (compose-go's merge-key walk spins
// on a self-merge up to its visit limit, and its cycle check is quadratic
// in the visits of one anchor).
func TestCheckAliases(t *testing.T) {
	t.Parallel()
	// Each level holds ten aliases of the previous one: 11, 111, ...,
	// 111,111 nodes expanded.
	var b strings.Builder
	b.WriteString("a: &a [x, x, x, x, x, x, x, x, x, x]\n")
	for i, prev := range []string{"a", "b", "c", "d", "e"} {
		name := string(rune('b' + i))
		fmt.Fprintf(&b, "%s: &%s [%s]\n", name, name, strings.TrimSuffix(strings.Repeat("*"+prev+",", 10), ","))
	}
	bomb := b.String()
	for _, tc := range []struct {
		name, yaml, want string
	}{
		{"self merge", "x: &a {<<: *a}\n", "used inside its own anchor"},
		{"self merge list", "x: &a {<<: [*a]}\n", "used inside its own anchor"},
		{"self sequence", "x: &a [*a]\n", "used inside its own anchor"},
		{"nested self", "services:\n  s: &s\n    environment:\n      E: {<<: *s}\n", "used inside its own anchor"},
		{"alias flood", "x-a: &a {k: v}\nx-l: [" + strings.TrimSuffix(strings.Repeat("*a,", maxComposeAliases+1), ",") + "]\n", "more than 1000 YAML aliases"},
		{"alias bomb", bomb, "expands to more than"},
		{"plain merge", "x-a: &a {k: v}\nservices:\n  s:\n    <<: *a\n  t:\n    <<: [*a]\n    l: *a\n", ""},
		{"sibling anchors", "x-a: &a {k: v}\nx-b: &b {<<: *a, j: w}\ny: {<<: [*b, *a]}\n", ""},
		{"no aliases", "services:\n  s:\n    image: x\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := singleDocument(ComposeFile, []byte(tc.yaml))
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.HasPrefix(err.Error(), ComposeFile+": line ") {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestLintSelfMergeIsFast: the FuzzCheckOverride seed that spun in
// compose-go (seconds per load, minutes under -race) is a finding now.
func TestLintSelfMergeIsFast(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"x: &a {<<: *a}\n", "services:\n  serve: &s\n    <<: *s\n"} {
		start := time.Now()
		fs := lintWith(t, nil, []byte(s), VariantTraefik, nil)
		if !hasFinding(fs, SeverityError, "used inside its own anchor") {
			t.Fatalf("%q: %v", s, fs)
		}
		if d := time.Since(start); d > 30*time.Second {
			t.Fatalf("%q took %v", s, d)
		}
	}
}

func TestLoadWithin(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	defer close(release)
	_, err := loadWithin(10*time.Millisecond, func(context.Context) (*types.Project, error) {
		<-release // a loader that ignores its context
		return nil, nil
	})
	if !errors.Is(err, errComposeLoadTimeout) {
		t.Fatalf("hung load: %v", err)
	}
	_, err = loadWithin(time.Minute, func(context.Context) (*types.Project, error) { panic("boom") })
	if err == nil || !strings.Contains(err.Error(), "the compose loader failed on this input") {
		t.Fatalf("panicking load: %v", err)
	}
	want := &types.Project{Name: "x"}
	if p, err := loadWithin(time.Minute, func(context.Context) (*types.Project, error) { return want, nil }); err != nil || p != want {
		t.Fatalf("load: %v %v", p, err)
	}
}
