package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// Options configure New.
type Options struct {
	// Level is debug, info, warn or error (default info).
	Level string
	// Format is json or text (default json).
	Format string
	// Registry holds secret values to scrub; nil uses DefaultRegistry.
	Registry *Registry
	// LevelVar, when set, receives Level and becomes the handler's level,
	// so the level can be changed at runtime (config reload, D-200).
	LevelVar *slog.LevelVar
}

// ParseLevel parses a log level name.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unknown log level %q (use debug, info, warn or error)", s)
	}
}

// New returns a logger writing to w in the requested format, wrapped in the
// redaction handler.
func New(w io.Writer, opts Options) (*slog.Logger, error) {
	level, err := ParseLevel(opts.Level)
	if err != nil {
		return nil, err
	}
	hopts := &slog.HandlerOptions{Level: level}
	if opts.LevelVar != nil {
		opts.LevelVar.Set(level)
		hopts.Level = opts.LevelVar
	}
	var inner slog.Handler
	switch strings.ToLower(opts.Format) {
	case "", "json":
		inner = slog.NewJSONHandler(w, hopts)
	case "text":
		inner = slog.NewTextHandler(w, hopts)
	default:
		return nil, fmt.Errorf("unknown log format %q (use json or text)", opts.Format)
	}
	reg := opts.Registry
	if reg == nil {
		reg = DefaultRegistry
	}
	return slog.New(NewHandler(inner, reg)), nil
}
