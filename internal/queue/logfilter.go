package queue

import (
	"context"
	"log/slog"
)

// argsFilter drops attributes that could carry job payloads from River's
// log records (River does not log args today; this keeps it that way
// across upgrades). Records still pass through the redacting handler.
type argsFilter struct{ h slog.Handler }

var droppedKeys = map[string]bool{
	"args": true, "encoded_args": true, "metadata": true, "output": true,
	"job_args": true, "params": true,
}

func (f argsFilter) Enabled(ctx context.Context, l slog.Level) bool { return f.h.Enabled(ctx, l) }

func (f argsFilter) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		if keep, ok := filterAttr(a); ok {
			out.AddAttrs(keep)
		}
		return true
	})
	return f.h.Handle(ctx, out)
}

func filterAttr(a slog.Attr) (slog.Attr, bool) {
	if droppedKeys[a.Key] {
		return slog.Attr{}, false
	}
	if a.Value.Kind() != slog.KindGroup {
		return a, true
	}
	var kept []slog.Attr
	for _, g := range a.Value.Group() {
		if k, ok := filterAttr(g); ok {
			kept = append(kept, k)
		}
	}
	return slog.Attr{Key: a.Key, Value: slog.GroupValue(kept...)}, true
}

func (f argsFilter) WithAttrs(attrs []slog.Attr) slog.Handler {
	kept := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		if k, ok := filterAttr(a); ok {
			kept = append(kept, k)
		}
	}
	return argsFilter{f.h.WithAttrs(kept)}
}

func (f argsFilter) WithGroup(name string) slog.Handler {
	if droppedKeys[name] {
		return argsFilter{discardGroup{}}
	}
	return argsFilter{f.h.WithGroup(name)}
}

// discardGroup swallows everything logged under a dropped group.
type discardGroup struct{}

func (discardGroup) Enabled(context.Context, slog.Level) bool  { return false }
func (discardGroup) Handle(context.Context, slog.Record) error { return nil }
func (d discardGroup) WithAttrs([]slog.Attr) slog.Handler      { return d }
func (d discardGroup) WithGroup(string) slog.Handler           { return d }
