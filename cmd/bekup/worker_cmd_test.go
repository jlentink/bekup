package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerAndNoWorkerFlags(t *testing.T) {
	root := newRootCmd()
	serve, _, err := root.Find([]string{"serve"})
	if err != nil {
		t.Fatal(err)
	}
	f := serve.Flags().Lookup("no-worker")
	if f == nil || f.DefValue != "false" {
		t.Fatalf("serve --no-worker missing or not default false: %+v", f)
	}
	worker, _, err := root.Find([]string{"worker"})
	if err != nil || worker.Name() != "worker" {
		t.Fatalf("worker command missing: %v", err)
	}
	if worker.DisableFlagParsing || strings.Contains(worker.Short, "not implemented") {
		t.Fatal("worker is still a stub")
	}
	if worker.Flags().Lookup("no-worker") != nil {
		t.Fatal("worker must not accept --no-worker")
	}
}

func TestWorkerRefusesInvalidConfig(t *testing.T) {
	_, _, err := run(t, "--config", filepath.Join(t.TempDir(), "missing.toml"), "worker")
	if err == nil || !strings.Contains(err.Error(), "invalid configuration") {
		t.Fatalf("got %v", err)
	}
	_, _, err = run(t, "worker", "extra")
	if err == nil {
		t.Fatal("worker accepted a positional argument")
	}
}
