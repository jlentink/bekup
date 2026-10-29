package app

import (
	"net/url"

	"github.com/jlentink/bekup/internal/graph/resolvers"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/notify/email"
)

// newEmail builds the e-mail services (task 5.1, D-178): SMTP settings
// (TLS follows the server, D-180), the sender behind the SMTP SSRF guard
// (scope smtp; loopback allowlistable only in dev mode), the admin test
// e-mail and the user e-mail verification with links built from
// server.base_url.
func (a *App) newEmail() (resolvers.EmailServices, error) {
	cfg := a.opts.Config
	guard := a.guards.Guard(netguard.ScopeSMTP)
	settings, err := email.NewSettings(email.SettingsOptions{
		DB: a.pool, Keyring: a.keyring, Auditor: a.auditor, Guard: guard, Logger: a.opts.Logger,
	})
	if err != nil {
		return resolvers.EmailServices{}, err
	}
	helo := ""
	if u, err := url.Parse(cfg.Server.BaseURL); err == nil {
		helo = u.Hostname()
	}
	sender, err := email.NewSender(email.SenderOptions{
		Settings: settings, Guard: guard, HeloName: helo, Logger: a.opts.Logger,
	})
	if err != nil {
		return resolvers.EmailServices{}, err
	}
	test, err := email.NewTestMailer(email.TestMailerOptions{Sender: sender, Auditor: a.auditor})
	if err != nil {
		return resolvers.EmailServices{}, err
	}
	verifier, err := email.NewVerifier(email.VerifierOptions{
		DB: a.pool, Keyring: a.keyring, Auditor: a.auditor, Mailer: sender,
		BaseURL: cfg.Server.BaseURL, Logger: a.opts.Logger,
	})
	if err != nil {
		return resolvers.EmailServices{}, err
	}
	a.mailer = sender
	return resolvers.EmailServices{Settings: settings, Test: test, Verifier: verifier}, nil
}
