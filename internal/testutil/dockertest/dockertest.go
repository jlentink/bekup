// Package dockertest holds the skip policy for tests that need a Docker
// daemon (integration tests of the runner, the graph harnesses, the app DB
// TLS checks, ...).
//
// Locally a missing or unsuitable Docker daemon skips the test, so
// `just test-int` works on machines without Docker. In CI (the CI
// environment variable is set to a true value, as GitHub Actions does) the
// same condition FAILS the test: a silently skipped integration suite would
// otherwise look green while testing nothing.
package dockertest

import (
	"fmt"
	"os"
	"strconv"
	"testing"
)

// InCI reports whether the tests run in CI (CI=true, 1, ...).
func InCI() bool {
	v, err := strconv.ParseBool(os.Getenv("CI"))
	return err == nil && v
}

// Skipf skips the test with the formatted reason, or fails it in CI. Use
// it for every "Docker is not available / not suitable" condition instead
// of t.Skipf.
func Skipf(t testing.TB, format string, args ...any) {
	t.Helper()
	if InCI() {
		t.Fatalf("docker required in CI: %s", fmt.Sprintf(format, args...))
		return
	}
	t.Skipf(format, args...)
}
