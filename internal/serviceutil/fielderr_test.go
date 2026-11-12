package serviceutil

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jlentink/bekup/internal/i18n"
)

func TestFieldErrFrom(t *testing.T) {
	t.Parallel()
	fallback := i18n.M("the host is not allowed")
	var fe *FieldError
	// A source with a message: its message is used, its English text never
	// passed on verbatim from err.Error().
	err := FieldErrFrom("host", fmt.Errorf("check: %w", i18n.Errorf("the host cannot be resolved")), fallback)
	if !errors.As(err, &fe) || fe.Field != "host" || fe.Msg.Format != "the host cannot be resolved" || fe.Message != "the host cannot be resolved" {
		t.Fatalf("with a message: %+v", fe)
	}
	// A plain error: the fallback, never its text.
	err = FieldErrFrom("host", errors.New("dial tcp 10.0.0.1:443: secret detail"), fallback)
	if !errors.As(err, &fe) || fe.Msg.Format != fallback.Format || fe.Message != fallback.String() {
		t.Fatalf("plain error: %+v", fe)
	}
	err = FieldErr("port", "the port must be at most %d", 65535)
	if !errors.As(err, &fe) || fe.Message != "the port must be at most 65535" || len(fe.Msg.Args) != 1 {
		t.Fatalf("FieldErr: %+v", fe)
	}
}
