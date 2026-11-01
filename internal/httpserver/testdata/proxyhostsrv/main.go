// Command proxyhostsrv is the container side of the trusted proxy hosts
// integration test (D-215): `serve <name>` runs an httpserver.Server in the
// own-proxy join shape (wildcard listener filtered to serve's address on
// the proxy network, alias bekup-serve) that trusts the container <name>
// by its name (Docker's embedded DNS) and answers /api/whoami with the
// client address it resolved; `get <url>` sends a request with
// X-Forwarded-For and prints the answer; `try <url>` sends one request and
// prints the body or ERR. Built for linux by the test; never shipped.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/jlentink/bekup/internal/httpserver"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: proxyhostsrv serve <proxy name> | get <url> [tries]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2])
	case "get":
		tries := 20
		if len(os.Args) > 3 {
			if _, err := fmt.Sscan(os.Args[3], &tries); err != nil {
				tries = 20
			}
		}
		err = get(os.Args[2], tries)
	case "try":
		try(os.Args[2])
	default:
		err = fmt.Errorf("unknown mode %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serve(proxy string) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, httpserver.ClientIP(r.Context()).String())
	})
	s, err := httpserver.New(httpserver.Options{
		BaseURL: "https://backup.example.com", MaxBodyBytes: 1024, Logger: logger,
		Ready:             func(context.Context) error { return nil },
		RateLimit:         httpserver.RateLimitOptions{PerSecond: 1000, Burst: 1000, MaxClients: 100},
		TrustedProxyHosts: []string{proxy},
		API:               api,
		OwnProxy:          true,
		JoinMode:          true,
		SelfConflict: func(_ context.Context, foreign []netip.Addr) error {
			fmt.Fprintf(os.Stderr, "SELF CONFLICT REPORTED %v\n", foreign)
			return nil
		},
	})
	if err != nil {
		return err
	}
	go func() { _ = s.ProxyHosts().Run(context.Background()) }()
	ln, err := net.Listen("tcp", "0.0.0.0:8080")
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	return srv.Serve(s.WrapListener(ln))
}

// try asks url once and prints the body or ERR.
func try(url string) {
	c := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		fmt.Println("ERR")
		return
	}
	resp, err := c.Do(req)
	if err != nil {
		fmt.Println("ERR")
		return
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	_ = resp.Body.Close()
	fmt.Println(strings.TrimSpace(string(b)))
}

// get asks url once a second (at most tries times) and prints every
// distinct answer on its own line; it stops at the forwarded address.
func get(url string, tries int) error {
	seen := map[string]bool{}
	c := &http.Client{Timeout: 3 * time.Second}
	for range tries {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("X-Forwarded-For", "198.51.100.9")
		resp, err := c.Do(req)
		if err == nil {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
			_ = resp.Body.Close()
			got := strings.TrimSpace(string(b))
			if !seen[got] {
				seen[got] = true
				fmt.Println(got)
			}
			if got == "198.51.100.9" {
				return nil
			}
		}
		time.Sleep(time.Second)
	}
	return nil
}
