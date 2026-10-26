package web

import (
	"io/fs"
	"testing"
)

func TestDistFSIsRootedAtDist(t *testing.T) {
	t.Parallel()

	if _, err := fs.Stat(DistFS(), ".gitkeep"); err != nil {
		t.Fatalf("expected .gitkeep at the root of DistFS: %v", err)
	}
	if _, err := fs.Stat(DistFS(), "dist"); err == nil {
		t.Fatal("DistFS must be rooted inside dist, not contain it")
	}
}
