package serviceutil

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestFieldErr(t *testing.T) {
	err := FieldErr("ssh.port", "port %d out of range", 70000)
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Field != "ssh.port" || fe.Message != "port 70000 out of range" {
		t.Fatalf("err = %#v", err)
	}
	if err.Error() != "invalid ssh.port: port 70000 out of range" {
		t.Errorf("Error() = %q", err.Error())
	}
}

func TestLimiter(t *testing.T) {
	allowed := true
	l, err := NewLimiter(1, func(string) (bool, time.Duration) { return allowed, 3 * time.Second })
	if err != nil {
		t.Fatal(err)
	}
	release, err := l.Acquire("u")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Acquire("u"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second acquire = %v, want ErrBusy", err)
	}
	release()
	allowed = false
	var rl *RateLimitedError
	if _, err := l.Acquire("u"); !errors.As(err, &rl) || rl.RetryAfter != 3*time.Second {
		t.Fatalf("limited acquire = %v", err)
	}
}

func TestDefaultLimiter(t *testing.T) {
	l, err := NewLimiter(0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cap(l.sem) != DefaultMaxConcurrent {
		t.Errorf("concurrency = %d", cap(l.sem))
	}
	n := 0
	for range DefaultBurst + 2 {
		release, err := l.Acquire("u")
		if err != nil {
			break
		}
		release()
		n++
	}
	if n != DefaultBurst {
		t.Errorf("burst = %d, want %d", n, DefaultBurst)
	}
	if release, err := l.Acquire("other"); err != nil {
		t.Errorf("another user limited: %v", err)
	} else {
		release()
	}
}

func TestIsUniqueViolation(t *testing.T) {
	if !IsUniqueViolation(fmt.Errorf("insert: %w", &pgconn.PgError{Code: "23505"})) {
		t.Error("23505 not detected")
	}
	if IsUniqueViolation(&pgconn.PgError{Code: "23503"}) || IsUniqueViolation(errors.New("x")) || IsUniqueViolation(nil) {
		t.Error("false positive")
	}
}
