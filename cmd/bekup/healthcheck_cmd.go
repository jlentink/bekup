package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// healthcheckTimeout bounds the whole probe (compose allows 5s).
const healthcheckTimeout = 3 * time.Second

func newHealthcheckCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "healthcheck",
		Short: "Probe a local bekup component (container healthchecks; exit 0 healthy, 1 unhealthy)",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "serve",
		Short: "GET /healthz on the address from server.listen (no shell or curl needed)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			res, err := config.LoadBekup(g.options(c))
			if err != nil {
				return fmt.Errorf("invalid configuration:\n%w", err)
			}
			u, err := healthzURL(res.Config.Server.Listen)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(c.Context(), healthcheckTimeout)
			defer cancel()
			if err := probe(ctx, u); err != nil {
				return err
			}
			_, err = fmt.Fprintln(c.OutOrStdout(), "ok")
			return err
		},
	}, &cobra.Command{
		Use:   "runner",
		Short: "Query the runner's local health socket (listen.health_socket; no client certificate needed)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			res, err := config.LoadRunner(config.Options{ConfigPath: g.configPath, Dev: g.dev})
			if err != nil {
				return fmt.Errorf("invalid runner configuration:\n%w", err)
			}
			path := res.Config.Listen.HealthSocket
			if path == "" {
				return errors.New("unhealthy: listen.health_socket is disabled in runner.toml")
			}
			ctx, cancel := context.WithTimeout(c.Context(), healthcheckTimeout)
			defer cancel()
			if err := probeRunner(ctx, path); err != nil {
				return err
			}
			_, err = fmt.Fprintln(c.OutOrStdout(), "ok")
			return err
		},
	})
	return cmd
}

// healthzURL derives the local /healthz URL from server.listen. A wildcard
// or empty host is probed on loopback (IPv6 loopback for "::").
func healthzURL(listen string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		return "", fmt.Errorf("server.listen %q is not host:port", listen)
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	default:
		if a, err := netip.ParseAddr(host); err == nil && a.Unmap().IsUnspecified() {
			host = "::1"
			if a.Is4In6() {
				host = "127.0.0.1"
			}
		}
	}
	return "http://" + net.JoinHostPort(host, port) + "/healthz", nil
}

// probe GETs u without proxies or redirects and requires a 200.
func probe(ctx context.Context, u string) error {
	client := &http.Client{
		Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("unhealthy: %w", err)
	}
	_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
	err = errors.Join(err, resp.Body.Close())
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: GET %s returned %d", u, resp.StatusCode)
	}
	if err != nil {
		return fmt.Errorf("unhealthy: read response: %w", err)
	}
	return nil
}

// probeRunner asks the standard gRPC health service on the runner's
// container-local unix socket (D-125) for runner.v1.RunnerService. The
// socket is plaintext: it is reachable only inside the runner container and
// serves nothing but the health status.
func probeRunner(ctx context.Context, socketPath string) (retErr error) {
	conn, err := grpc.NewClient("unix://"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithNoProxy(),
		grpc.WithDisableRetry(),
		grpc.WithDisableServiceConfig())
	if err != nil {
		return fmt.Errorf("unhealthy: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, conn.Close()) }()
	resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{Service: runnerpb.ServiceName})
	if err != nil {
		return fmt.Errorf("unhealthy: %w", err)
	}
	if st := resp.GetStatus(); st != healthpb.HealthCheckResponse_SERVING {
		return fmt.Errorf("unhealthy: runner status %s", st)
	}
	return nil
}
