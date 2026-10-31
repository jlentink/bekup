package wizard

import "errors"

// errRepeat shows the same step again (a choice that cannot be used yet).
var errRepeat = errors.New("repeat")

// step is one screen (or a short sequence of screens) of a wizard.
type step[S any] struct {
	name string
	// skip hides the step for the current answers.
	skip func(S) bool
	run  func(S) error
}

// runSteps runs the steps in order. ErrBack returns to the previous step
// that ran (the first step stays where it is); any other error ends the
// wizard.
func runSteps[S any](st S, steps []step[S]) error {
	var history []int
	for i := 0; i < len(steps); {
		s := steps[i]
		if s.skip != nil && s.skip(st) {
			i++
			continue
		}
		err := s.run(st)
		switch {
		case errors.Is(err, errRepeat):
		case errors.Is(err, ErrBack):
			if len(history) > 0 {
				i = history[len(history)-1]
				history = history[:len(history)-1]
			}
		case err != nil:
			return err
		default:
			history = append(history, i)
			i++
		}
	}
	return nil
}
