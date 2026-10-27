//go:build !integration

package tunnel

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the unit tests if a tunnel leaks goroutines. The
// integration build checks leaks per test instead (testcontainers keeps
// background goroutines).
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
