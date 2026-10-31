package wizard

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/setup"
)

// Renew actions.
const (
	renewDue   = "due"
	renewAll   = "all"
	renewPhase = "phase"
	renewQuit  = "quit"
)

// RunRenew is the guided `bekup setup renew`: it lists the certificates,
// asks what to renew (or which CA rotation phase to run), and returns the
// options of setup.Renew; their Confirm hook shows the plan and asks
// again, their Passphrase hook asks for the CA key's passphrase. done is
// true when there is nothing to do. warnDays is the warning period (0:
// certs.ExpiryWarnDays, the threshold of the hourly security check).
func RunRenew(ui UI, dir string, now time.Time, warnDays int) (opts setup.RenewOptions, done bool, err error) {
	p, err := setup.InspectCertificates(dir, now, warnDays)
	if err != nil {
		return opts, false, err
	}
	var b strings.Builder
	for _, c := range p.Certificates {
		state := "ok"
		switch {
		case c.Expired:
			state = "EXPIRED"
		case c.Expiring:
			state = "expires soon"
		}
		fmt.Fprintf(&b, "%-8s until %s  %s\n", c.Component, c.NotAfter.Format("2006-01-02"), state)
	}
	// Due counts missing leaves too (they are issued by the renewal).
	dueList := p.Due()
	due := len(dueList)
	for _, c := range dueList {
		if !slices.ContainsFunc(p.Certificates, func(cs setup.CertStatus) bool { return cs.Component == c }) {
			fmt.Fprintf(&b, "%-8s MISSING\n", c)
		}
	}
	fmt.Fprintf(&b, "\nWarning from %d days before expiry", p.WarnDays)
	if p.WarnDays == certs.ExpiryWarnDays {
		b.WriteString(" (as the hourly security check)")
	}
	fmt.Fprintf(&b, ".\nCA rotation: %s", p.Rotation)
	if !p.CAMounted {
		fmt.Fprintf(&b, "\nThe CA is not mounted at %s: every renewal needs it.", p.CADir)
	}
	if err := ui.Note("Certificates", b.String()); err != nil {
		return opts, false, err
	}
	var choices []Option
	switch p.Rotation {
	case setup.RotationStaged, setup.RotationPromoted:
		choices = append(choices, Option{renewPhase, fmt.Sprintf("continue the CA rotation: %s (after every service was recreated)", p.Rotation.NextPhase())})
	default:
		if due > 0 {
			choices = append(choices, Option{renewDue, fmt.Sprintf("renew the %d expiring or missing certificate(s): %s", due, strings.Join(dueList, ", "))})
		}
		choices = append(choices, Option{renewAll, "renew every leaf certificate (" + strings.Join(p.Components, ", ") + ")"})
		if p.Rotation == setup.RotationNone {
			choices = append(choices, Option{renewPhase, "rotate the CA itself (3 phases: stage now, promote and finish in later runs)"})
		}
	}
	choices = append(choices, Option{renewQuit, "nothing, quit"})
	def := renewQuit
	if due > 0 || p.Rotation == setup.RotationStaged || p.Rotation == setup.RotationPromoted {
		def = choices[0].Value
	}
	action, err := ui.Select("Renew", "What should be renewed? The CA key stays offline; mount it at "+p.CADir+" for this run.", choices, def)
	if err != nil {
		return opts, false, err
	}
	opts = setup.RenewOptions{Dir: dir, WarnDays: p.WarnDays, Out: ui.Out(), Now: func() time.Time { return now }}
	switch action {
	case renewQuit:
		return opts, true, nil
	case renewAll:
		opts.All = true
	case renewPhase:
		opts.CAPhase = p.Rotation.NextPhase()
		if p.Rotation == setup.RotationUnknown {
			opts.CAPhase = certs.CAStage
		}
	}
	opts.Confirm = func(plan *setup.RenewPlan) error {
		var b strings.Builder
		if plan.Phase != "" {
			fmt.Fprintf(&b, "CA rotation phase: %s\n", plan.Phase)
		} else {
			fmt.Fprintf(&b, "Renew: %s\n", strings.Join(plan.Renew, ", "))
		}
		fmt.Fprintf(&b, "Afterwards: %s", plan.RestartCommand())
		ok, err := ui.Confirm("Renew now?", b.String(), true)
		if err != nil {
			return err
		}
		if !ok {
			return ErrAborted
		}
		return nil
	}
	opts.Passphrase = func() ([]byte, error) {
		pass, err := ui.Secret("Passphrase of the CA key", "The CA key mounted at "+p.CADir+".", nil)
		if errors.Is(err, ErrBack) {
			return nil, ErrAborted
		}
		return pass, err
	}
	return opts, false, nil
}
