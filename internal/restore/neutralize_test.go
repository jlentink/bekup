package restore

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/runnerpb"
)

// The neutralization of restored PostgreSQL databases (M4 review): it runs
// after pg_restore and before the swap or the success, and a failure stops
// the restore with the target untouched.
func TestPGNeutralize(t *testing.T) {
	old := []byte("old data")
	dump := dumpOf(64 << 10)
	const temp, oldName = "app__bekup_restore_0123456789ab", "app__bekup_old_0123456789ab"
	setup := func() *FakeRunner {
		f := NewFakeRunner("app")
		f.Received["app"] = bytes.Clone(old)
		return f
	}
	untouched := func(t *testing.T, f *FakeRunner) {
		t.Helper()
		if !f.Has("app") || !bytes.Equal(f.Data("app"), old) || f.Has(temp) {
			t.Fatal("the target changed or the temporary database was kept")
		}
		for _, c := range f.CallList() {
			if strings.HasPrefix(c, "swap:") {
				t.Fatalf("unexpected call %s", c)
			}
		}
	}

	t.Run("overwrite neutralizes before the swap", func(t *testing.T) {
		f := setup()
		f.Secdef = []string{"public.esc()", "app.\"x\nevil\"(integer)"}
		req := pgReq(f, ModeOverwrite, "app", dump, nil)
		var seen *Neutralized
		req.OnNeutralized = func(_ context.Context, n Neutralized) error {
			if slices.Contains(f.CallList(), "swap:app") {
				t.Error("OnNeutralized ran after the swap")
			}
			seen = &n
			return nil
		}
		logs := &memSink{}
		res, err := (pgRestorer{}).Restore(t.Context(), req, bytes.NewReader(dump), logs)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"create:" + temp, "restore:" + temp, "neutralize:" + temp, "swap:app", "drop:" + oldName}
		if got := f.CallList(); !slices.Equal(got, want) {
			t.Fatalf("calls = %v", got)
		}
		if seen == nil || seen.Count != 2 || res.Neutralized == nil || res.Neutralized.Count != 2 {
			t.Fatalf("neutralized = %+v / %+v", seen, res.Neutralized)
		}
		if !slices.Contains(res.Warnings, WarnSecdefNeutralized) {
			t.Fatalf("warnings = %v", res.Warnings)
		}
		if strings.Contains(seen.Functions[1], "\n") {
			t.Fatalf("control characters kept: %q", seen.Functions[1])
		}
		if !strings.Contains(logs.text(), "switched to SECURITY INVOKER: public.esc()") {
			t.Fatalf("log:\n%s", logs.text())
		}
		if OutcomeWarningMessage(WarnSecdefNeutralized) == "Warning: "+WarnSecdefNeutralized+"." {
			t.Fatal("no outcome text for the warning")
		}
	})
	t.Run("nothing to neutralize is no warning", func(t *testing.T) {
		f := setup()
		called := false
		req := pgReq(f, ModeOverwrite, "app", dump, nil)
		req.OnNeutralized = func(context.Context, Neutralized) error { called = true; return nil }
		res, err := (pgRestorer{}).Restore(t.Context(), req, bytes.NewReader(dump), &memSink{})
		if err != nil || slices.Contains(res.Warnings, WarnSecdefNeutralized) || !called || res.Neutralized == nil {
			t.Fatalf("%+v %v %v", res, err, called)
		}
	})
	t.Run("neutralize failure stops before the swap", func(t *testing.T) {
		f := setup()
		f.NeutralizeErr = runnerpb.ReasonPermission.Status()
		logs := &memSink{}
		_, err := (pgRestorer{}).Restore(t.Context(), pgReq(f, ModeOverwrite, "app", dump, nil), bytes.NewReader(dump), logs)
		if !errors.Is(err, ErrNeutralizeFailed) || ErrorCode(err) != CodeNeutralizeFailed {
			t.Fatalf("err = %v (%s)", err, ErrorCode(err))
		}
		untouched(t, f)
		if !strings.Contains(logs.text(), "is unchanged") {
			t.Fatalf("log:\n%s", logs.text())
		}
	})
	t.Run("audit failure stops before the swap", func(t *testing.T) {
		f := setup()
		f.Secdef = []string{"public.esc()"}
		req := pgReq(f, ModeOverwrite, "app", dump, nil)
		req.OnNeutralized = func(context.Context, Neutralized) error { return ErrAuditFailed }
		_, err := (pgRestorer{}).Restore(t.Context(), req, bytes.NewReader(dump), &memSink{})
		if !errors.Is(err, ErrAuditFailed) {
			t.Fatalf("err = %v", err)
		}
		untouched(t, f)
	})
	t.Run("pin mismatch keeps its code", func(t *testing.T) {
		f := setup()
		f.NeutralizeErr = runnerpb.PinMismatchStatus("leaf")
		_, err := (pgRestorer{}).Restore(t.Context(), pgReq(f, ModeOverwrite, "app", dump, nil), bytes.NewReader(dump), &memSink{})
		if ErrorCode(err) != "tls_pin_mismatch" || runnerpb.ErrorReason(err) != runnerpb.ReasonPinMismatch {
			t.Fatalf("err = %v (%s)", err, ErrorCode(err))
		}
		untouched(t, f)
	})
	t.Run("new database", func(t *testing.T) {
		f := setup()
		f.Secdef = []string{"public.esc()"}
		res, err := (pgRestorer{}).Restore(t.Context(), pgReq(f, ModeNewDB, "copy", dump, nil), bytes.NewReader(dump), &memSink{})
		if err != nil || !slices.Contains(res.Warnings, WarnSecdefNeutralized) {
			t.Fatalf("%+v %v", res, err)
		}
		if !slices.Contains(f.CallList(), "neutralize:copy") || f.Headers[0].GetNoPrivileges() {
			t.Fatalf("calls = %v, header %v", f.CallList(), f.Headers[0])
		}
		f2 := setup()
		f2.NeutralizeErr = status.Error(codes.Unavailable, "down")
		logs := &memSink{}
		res, err = (pgRestorer{}).Restore(t.Context(), pgReq(f2, ModeNewDB, "copy", dump, nil), bytes.NewReader(dump), logs)
		if ErrorCode(err) != CodeNeutralizeFailed || !slices.Contains(res.Warnings, WarnPartialNewDatabase) {
			t.Fatalf("%+v %v (%s)", res, err, ErrorCode(err))
		}
		if !strings.Contains(logs.text(), "without PUBLIC access") {
			t.Fatalf("log:\n%s", logs.text())
		}
	})
	t.Run("scratch database", func(t *testing.T) {
		f := setup()
		const scratch = "bekup_test_0123456789ab"
		if _, err := (pgRestorer{}).Restore(t.Context(), pgReq(f, ModeNewDB, scratch, dump, nil), bytes.NewReader(dump), &memSink{}); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(f.CallList(), "neutralize:"+scratch) {
			t.Fatalf("calls = %v", f.CallList())
		}
		if h := f.Headers[0]; !h.GetNoPrivileges() || !h.GetNoOwner() {
			t.Fatalf("scratch header %v", h)
		}
	})
	t.Run("canceled after neutralizing, before the swap", func(t *testing.T) {
		f := setup()
		ctx, cancel := context.WithCancel(t.Context())
		req := pgReq(f, ModeOverwrite, "app", dump, nil)
		// Builds: create, restore, neutralize (released after the RPC).
		req.Target = cancelAfter(req.Target, 3, cancel)
		_, err := (pgRestorer{}).Restore(ctx, req, bytes.NewReader(dump), &memSink{})
		if err == nil {
			t.Fatal("no error")
		}
		untouched(t, f)
	})
}

// cancelAfter cancels after the n-th target build has been released.
func cancelAfter(tb TargetBuilder, n int, cancel context.CancelFunc) TargetBuilder {
	built := 0
	return func(ctx context.Context) (*runnerpb.Target, func(), error) {
		t, release, err := tb(ctx)
		if err != nil {
			return t, release, err
		}
		built++
		k := built
		return t, func() {
			release()
			if k == n {
				cancel()
			}
		}, nil
	}
}

// A failed swap call is resolved through the runner's database list:
// swapped, unchanged, or reported as unknown, never "unchanged" without
// proof.
func TestPGSwapState(t *testing.T) {
	old := []byte("old data")
	dump := dumpOf(32 << 10)
	const temp, oldName = "app__bekup_restore_0123456789ab", "app__bekup_old_0123456789ab"
	setup := func() *FakeRunner {
		f := NewFakeRunner("app")
		f.Received["app"] = bytes.Clone(old)
		return f
	}
	t.Run("applied swap with a lost answer is a success", func(t *testing.T) {
		f := setup()
		f.SwapAppliedErr = status.Error(codes.Unavailable, "connection reset")
		logs := &memSink{}
		_, err := (pgRestorer{}).Restore(t.Context(), pgReq(f, ModeOverwrite, "app", dump, nil), bytes.NewReader(dump), logs)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(f.Data("app"), dump) || f.Has(oldName) || f.Has(temp) {
			t.Fatal("the old database was not dropped after the swap")
		}
		if !strings.Contains(logs.text(), "the server shows it happened") || strings.Contains(logs.text(), "unchanged") {
			t.Fatalf("log:\n%s", logs.text())
		}
	})
	t.Run("failed swap leaves the target", func(t *testing.T) {
		f := setup()
		f.SwapErr = runnerpb.ReasonFailed.Status()
		logs := &memSink{}
		_, err := (pgRestorer{}).Restore(t.Context(), pgReq(f, ModeOverwrite, "app", dump, nil), bytes.NewReader(dump), logs)
		if ErrorCode(err) != "target_failed" || !strings.Contains(logs.text(), "the target database is unchanged") {
			t.Fatalf("err = %v\n%s", err, logs.text())
		}
		if !bytes.Equal(f.Data("app"), old) || f.Has(temp) {
			t.Fatal("target changed or temp kept")
		}
	})
	t.Run("unknown state, swap not applied", func(t *testing.T) {
		f := setup()
		f.SwapErr = status.Error(codes.Unavailable, "down")
		f.ProbeErr = status.Error(codes.Unavailable, "down")
		logs := &memSink{}
		_, err := (pgRestorer{}).Restore(t.Context(), pgReq(f, ModeOverwrite, "app", dump, nil), bytes.NewReader(dump), logs)
		if !errors.Is(err, ErrSwapStateUnknown) || ErrorCode(err) != CodeSwapStateUnknown {
			t.Fatalf("err = %v (%s)", err, ErrorCode(err))
		}
		// The temporary database still existed: that proves the swap had
		// not happened.
		if !strings.Contains(logs.text(), "app is unchanged") {
			t.Fatalf("log:\n%s", logs.text())
		}
	})
	t.Run("unknown state, swap applied", func(t *testing.T) {
		f := setup()
		f.SwapAppliedErr = status.Error(codes.Unavailable, "down")
		f.ProbeErr = status.Error(codes.Unavailable, "down")
		logs := &memSink{}
		_, err := (pgRestorer{}).Restore(t.Context(), pgReq(f, ModeOverwrite, "app", dump, nil), bytes.NewReader(dump), logs)
		if ErrorCode(err) != CodeSwapStateUnknown {
			t.Fatalf("err = %v (%s)", err, ErrorCode(err))
		}
		if strings.Contains(logs.text(), "unchanged") || !strings.Contains(logs.text(), "probably happened") {
			t.Fatalf("log:\n%s", logs.text())
		}
		if !f.Has(oldName) {
			t.Fatal("the replaced database must be kept when the state is unknown")
		}
	})
}

func TestCleanSignature(t *testing.T) {
	long := strings.Repeat("é", 300)
	for in, want := range map[string]string{
		"public.f(integer)": "public.f(integer)",
		"a\x00b\nc\x7f":     "a?b?c?",
		"bad\xffutf8":       "bad?utf8",
		long:                strings.Repeat("é", maxNeutralizedSignature/2),
		`"x;y"."z"(text[])`: `"x;y"."z"(text[])`,
		"":                  "",
		"tab\there":         "tab?here",
		" line separator":   " line separator",
	} {
		if got := cleanSignature(in); got != want {
			t.Errorf("cleanSignature(%q) = %q, want %q", in, got, want)
		}
		if len(cleanSignature(in)) > maxNeutralizedSignature {
			t.Errorf("too long: %d", len(cleanSignature(in)))
		}
	}
}
