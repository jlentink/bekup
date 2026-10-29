package streams

import "context"

// Feed is a prepared subscription source: Deliver's arguments (the
// identifier-only events, the delivery-time check and the conversion).
// Combined subscriptions (D-197) build one Feed per event kind and merge
// them with Combine, so every kind keeps its own check.
type Feed[In, Out any] struct {
	Src   <-chan In
	Check func(context.Context, In) error
	Conv  func(context.Context, In) (Out, bool)
}

// Deliver runs Deliver for the feed.
func (f Feed[In, Out]) Deliver(ctx context.Context) <-chan Out {
	return Deliver(ctx, f.Src, f.Check, f.Conv)
}

// Either is one event of a combined feed: A, or B when IsB.
type Either[A, B any] struct {
	A   A
	B   B
	IsB bool
}

// CombineOptions tune Combine.
type CombineOptions[A any] struct {
	// EndAfterB ends the combined source once B's source closed (e.g. a
	// log stream that completed). When set, its result (if ok) is sent
	// as a last A event first; it runs on the merge goroutine.
	EndAfterB func() (A, bool)
}

// Combine merges two feeds into one: a single source (so one stream and
// one session check per event) whose events are checked and converted
// by the feed they came from, and wrapped into Out by wa / wb. The source
// closes when ctx ends, when both sources closed, or (EndAfterB) once B
// closed. A nil source counts as closed from the start (without
// triggering EndAfterB).
func Combine[A, B, AO, BO, Out any](ctx context.Context, fa Feed[A, AO], fb Feed[B, BO],
	wa func(AO) Out, wb func(BO) Out, opts CombineOptions[A],
) Feed[Either[A, B], Out] {
	src := make(chan Either[A, B])
	go mergeSources(ctx, fa.Src, fb.Src, src, opts.EndAfterB)
	return Feed[Either[A, B], Out]{
		Src: src,
		Check: func(ctx context.Context, ev Either[A, B]) error {
			if ev.IsB {
				return fb.Check(ctx, ev.B)
			}
			return fa.Check(ctx, ev.A)
		},
		Conv: func(ctx context.Context, ev Either[A, B]) (Out, bool) {
			var zero Out
			if ev.IsB {
				v, ok := fb.Conv(ctx, ev.B)
				if !ok {
					return zero, false
				}
				return wb(v), true
			}
			v, ok := fa.Conv(ctx, ev.A)
			if !ok {
				return zero, false
			}
			return wa(v), true
		},
	}
}

func mergeSources[A, B any](ctx context.Context, a <-chan A, b <-chan B, out chan<- Either[A, B], endAfterB func() (A, bool)) {
	defer close(out)
	send := func(ev Either[A, B]) bool {
		select {
		case out <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}
	for a != nil || b != nil {
		select {
		case <-ctx.Done():
			return
		case v, ok := <-a:
			if !ok {
				a = nil
				continue
			}
			if !send(Either[A, B]{A: v}) {
				return
			}
		case v, ok := <-b:
			if !ok {
				b = nil
				if endAfterB != nil {
					if last, ok := endAfterB(); ok {
						send(Either[A, B]{A: last})
					}
					return
				}
				continue
			}
			if !send(Either[A, B]{B: v, IsB: true}) {
				return
			}
		}
	}
}
