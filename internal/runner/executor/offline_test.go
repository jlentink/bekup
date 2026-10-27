package executor

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/spec"
)

func verifyCommand(t *testing.T, ent engine.Entry) func(ClientEndpoint) (engine.Command, error) {
	t.Helper()
	eng, err := engine.ForKind(engine.KindPostgres)
	if err != nil {
		t.Fatal(err)
	}
	return func(ep ClientEndpoint) (engine.Command, error) {
		if ep != (ClientEndpoint{}) {
			t.Errorf("offline run got an endpoint: %+v", ep)
		}
		return eng.VerifyDump(ent)
	}
}

func TestRunOffline(t *testing.T) {
	fd := newFakeDocker()
	payload := bytes.Repeat([]byte("PGDMP-archive-"), 10_000)
	var got []byte
	fd.proc = func(p *fakeProc) {
		pre := p.readPreamble()
		if pre != "BEKUP-PREAMBLE 1\nEND\n" {
			t.Errorf("offline preamble = %q, want no records", pre)
		}
		var err error
		if got, err = p.readAll(); err != nil {
			t.Error(err)
		}
		discardErr(p.stdout([]byte("; Archive created\n")))
		p.exit(0)
	}
	e := newTestExecutor(t, fd)
	s := &sink{}
	ent := testEntry(t)
	before := len(fd.Calls())
	res, err := e.Run(t.Context(), RunSpec{
		RunID: testRunID, Entry: ent, Offline: true, Command: verifyCommand(t, ent),
		Stdin: bytes.NewReader(payload), Stdout: s.stdout, Log: s.log, Timeout: 10 * time.Second,
	})
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("stdin: got %d bytes, want %d", len(got), len(payload))
	}
	if s.data.String() != "; Archive created\n" {
		t.Fatalf("stdout = %q", s.data.String())
	}
	if m := string(fd.lastSpec.HostConfig.NetworkMode); m != spec.NetworkNone {
		t.Fatalf("network mode = %q, want none", m)
	}
	if len(fd.lastSpec.Networking.EndpointsConfig) != 0 || len(fd.lastSpec.HostConfig.ExtraHosts) != 0 {
		t.Fatal("offline container has endpoints or extra hosts")
	}
	for _, op := range fd.Calls()[before:] {
		switch op {
		case "NetworkCreate", "NetworkConnect", "NetworkList":
			t.Fatalf("offline run called %s", op)
		}
	}
	e.mu.Lock()
	left := len(e.offline)
	e.mu.Unlock()
	if left != 0 {
		t.Fatal("offline run not released")
	}
	checkReleased(t, e, fd)
}

func TestRunOfflineRejectsTargetsAndFiles(t *testing.T) {
	fd := newFakeDocker()
	e := newTestExecutor(t, fd)
	s := &sink{}
	ent := testEntry(t)
	base := RunSpec{RunID: testRunID, Entry: ent, Offline: true, Command: verifyCommand(t, ent), Stdout: s.stdout}

	withTarget := base
	withTarget.Target = Target{Host: dbHost, Port: 5432}
	if _, err := e.Run(t.Context(), withTarget); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("offline with target: %v", err)
	}
	withFiles := base
	withFiles.Command = pgCommand(t, ent, engine.TLSPinned) // needs pgpass + ca
	withFiles.Material = material()
	if _, err := e.Run(t.Context(), withFiles); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("offline with preamble files: %v", err)
	}
	checkReleased(t, e, fd)
}

func TestRunOfflineDuplicate(t *testing.T) {
	fd := newFakeDocker()
	release := make(chan struct{})
	fd.proc = func(p *fakeProc) {
		p.readPreamble()
		<-release
		p.exit(0)
	}
	e := newTestExecutor(t, fd)
	ent := testEntry(t)
	s := &sink{}
	done := make(chan error, 1)
	go func() {
		_, err := e.Run(t.Context(), RunSpec{RunID: testRunID, Entry: ent, Offline: true, Command: verifyCommand(t, ent), Stdout: s.stdout})
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.mu.Lock()
		n := len(e.offline)
		e.mu.Unlock()
		if n == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := e.Run(t.Context(), RunSpec{RunID: testRunID, Entry: ent, Offline: true, Command: verifyCommand(t, ent), Stdout: s.stdout}); !errors.Is(err, ErrDuplicateRun) {
		t.Fatalf("duplicate offline run: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	checkReleased(t, e, fd)
}
