package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/dbsetup"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/setup"
	setupweb "github.com/jlentink/bekup/internal/setup/web"
	"github.com/jlentink/bekup/internal/setup/wizard"
	"github.com/jlentink/bekup/web"
)

// Environment of the browser setup services (D-217).
const (
	envBrowserHost = "BEKUP_SETUP_WEB_HOST"
	envBrowserPort = "BEKUP_SETUP_WEB_PORT"
)

// setupWebRun is setupweb.Run (tests replace it).
var setupWebRun = setupweb.Run

func newSetupWebCmd() *cobra.Command {
	var (
		dir, ownership, listen string
		hosts                  []string
		publicPort             int
		database               bool
		allowVersionMismatch   bool
	)
	cmd := &cobra.Command{
		Use:   "web",
		Short: "Browser setup: the setup wizard over HTTPS with a one-time code (D-217)",
		Long: `Serves the setup wizard to one browser session over HTTPS (D-217): the
terminal shows the address with a one-time access code and the fingerprint
of the self-signed certificate; compare it before you continue in the
browser. Setup stops when it is finished, after 30 minutes without
activity, after 2 hours, or after 10 wrong codes.

Two runs, from the compose file:

  docker compose run --rm --service-ports setup-web      # questions, files, check
  docker compose up -d postgres                          # bundled database only
  docker compose run --rm --service-ports setup-web-db   # roles and migrations

The first run writes the deployment like ` + "`bekup setup`" + ` and offers the
non-secret files (compose, setup's .env variables, TOML, state) as a zip;
secrets never leave the server. The second (--database) runs as the bekup
user with migrate's configuration and secrets and, of the deployment
directory, only the state file: db bootstrap for your own PostgreSQL, then
migrate up.
The first admin is still created on the host (bekup admin create, D-113).`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			mode, err := setup.ParseOwnershipMode(ownership)
			if err != nil {
				return err
			}
			if h := strings.TrimSpace(os.Getenv(envBrowserHost)); h != "" && !c.Flags().Changed("host") {
				hosts = []string{h}
			}
			if !c.Flags().Changed("public-port") {
				if p := strings.TrimSpace(os.Getenv(envBrowserPort)); p != "" {
					if publicPort, err = strconv.Atoi(p); err != nil || publicPort < 1 || publicPort > 65535 {
						return fmt.Errorf("%s=%q: not a port", envBrowserPort, p)
					}
				}
			}
			logger := slog.New(logging.NewHandler(slog.NewTextHandler(c.ErrOrStderr(), nil), logging.DefaultRegistry))
			opts := setupweb.Options{
				Dir: dir, Listen: listen, Hosts: hosts, PublicPort: publicPort,
				Env: wizard.Env{
					DefaultDir: setupDefaultDir, Version: version, Image: strings.TrimSpace(os.Getenv(envSetupImage)),
					DockerGID: strings.TrimSpace(os.Getenv(envDockerGID)), AllowVersionMismatch: allowVersionMismatch,
					Ownership: mode,
				},
				Init:   setupInit,
				Check:  setup.Check,
				Assets: web.SetupFS(),
				Out:    c.OutOrStdout(),
				Logger: logger,
			}
			if database {
				opts.Mode = setupweb.ModeDatabase
				opts.Database = setupweb.DatabaseStep(setupweb.DatabaseOptions{
					Load:   func() (*config.Result[config.Bekup], error) { return config.LoadBekup(config.Options{}) },
					Guard:  dbsetup.Guard{Version: version, LookupEnv: lookupEnv},
					Logger: logger,
				})
			}
			ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return setupWebRun(ctx, opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&dir, "dir", setupDefaultDir, "deployment directory (must exist)")
	f.StringVar(&ownership, "ownership", string(setup.OwnershipAuto), "auto, chown, print or none")
	f.StringVar(&listen, "listen", ":"+strconv.Itoa(setupweb.ListenPort), "listen address inside the container")
	f.StringSliceVar(&hosts, "host", nil, "host name or address the browser opens setup with (certificate and printed URL; default "+envBrowserHost+")")
	f.IntVar(&publicPort, "public-port", setupweb.ListenPort, "published port shown in the URL (default "+envBrowserPort+" or 8443)")
	f.BoolVar(&database, "database", false, "the database run (setup-web-db service): db bootstrap and migrate up")
	f.BoolVar(&allowVersionMismatch, "allow-version-mismatch", false, "accept an image tag that differs from this bekup version")
	return cmd
}
