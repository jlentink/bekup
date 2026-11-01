package setup

import (
	"fmt"
	"strings"

	"github.com/jlentink/bekup/internal/imageref"
)

// Overlays are the setup-only compose overlays (D-215). They are never
// part of a release or committed compose file (those stay four: the
// traefik and own-proxy variants with the bundled or an external
// database): setup writes them into compose.override.yaml, which docker
// compose merges automatically, so compose.yaml stays the byte-exact
// release file and an upgrade stays a file replacement. setup check holds
// bekup's services to compose.yaml plus exactly these blocks (the lint's
// expected project is the release file merged with overlayDoc).
type Overlays struct {
	// ProxyLabels: the Traefik Docker-provider labels on serve (own-proxy).
	ProxyLabels bool
	// ProxyLink: serve's fixed address on the bekup-proxy link network
	// (own-proxy link mode, D-205).
	ProxyLink bool
	// DBNetwork: migrate, serve and admin join the database container's
	// Docker network (external, BEKUP_DB_NETWORK); backend stays internal.
	DBNetwork bool
}

// Any reports whether at least one overlay applies.
func (o Overlays) Any() bool { return o.ProxyLabels || o.ProxyLink || o.DBNetwork }

// overlayServeLabels is the Traefik label block on serve (no
// middlewares: bekup sets its own security headers, D-102; flushinterval
// -1ms streams the Server-Sent Events, D-124).
const overlayServeLabels = `    # Routing for YOUR Traefik (Docker provider with exposedByDefault=false;
    # docs/installation/reverse-proxy.md#traefik). No middlewares: bekup sets
    # its own security headers (D-102). flushinterval -1ms streams the live
    # updates (Server-Sent Events, D-124) without delay.
    labels:
      traefik.enable: "true"
      traefik.docker.network: ${BEKUP_PROXY_NETWORK:-bekup-proxy}
      traefik.http.routers.bekup.rule: "Host(` + "`${BEKUP_DOMAIN:-bekup.invalid}`" + `)"
      traefik.http.routers.bekup.entrypoints: ${TRAEFIK_ENTRYPOINT:-websecure}
      traefik.http.routers.bekup.tls: "true"
      traefik.http.routers.bekup.tls.certresolver: ${TRAEFIK_CERTRESOLVER:-letsencrypt}
      traefik.http.routers.bekup.service: bekup
      traefik.http.services.bekup.loadbalancer.server.port: "8080"
      traefik.http.services.bekup.loadbalancer.responseforwarding.flushinterval: "-1ms"
`

// overlayServeLink is serve's fixed address on the link network.
const overlayServeLink = `      proxy:
        # Link mode (D-205): the fixed address on the bekup-proxy link network
        # (SERVE_PROXY_IP), so [server] listen = "10.213.253.10:8080" binds this
        # interface only and trusted_proxies names your proxy's fixed address.
        ipv4_address: ${SERVE_PROXY_IP:-10.213.253.10}
`

// overlayDBJoin joins a service to the database container's network.
const overlayDBJoin = `      # YOUR database container's network (BEKUP_DB_NETWORK, D-215).
      database: {}
`

// overlayDBNetworks are the top-level networks of the DB-network overlay.
const overlayDBNetworks = `  # The app database is a container on YOUR Docker network: backend needs no
  # route off this host and stays internal.
  backend:
    internal: true
  # YOUR database container's network (external; setup writes its name to
  # .env as BEKUP_DB_NETWORK). Only migrate, serve and admin join it.
  database:
    external: true
    name: ${BEKUP_DB_NETWORK:-bekup-db}
`

// overlayHeader starts the override setup writes.
const overlayHeader = `# compose.override.yaml: written once by ` + "`bekup setup init`" + ` (D-203, D-215).
# Operator file: setup never rewrites it (not ` + "`setup init --force`" + `, not
# ` + "`setup upgrade`" + `). docker compose merges it into compose.yaml automatically
# (not with -f compose.yaml). setup check holds bekup's services to
# compose.yaml plus exactly the blocks setup wrote here; keep them, and merge
# your own changes into the existing services: and networks: keys below (a
# second services: or networks: key is invalid YAML).
`

// overlayDoc renders the overlay blocks as a compose document (services
// and networks only; "" without overlays). staticTLS adds the bundled
// proxy's certificate mount (proxy names traefik or nginx): an
// operator-tunable change that is not part of the lint's expected project.
func overlayDoc(o Overlays, staticTLSProxy Proxy) string {
	if !o.Any() && staticTLSProxy == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("services:\n")
	if o.DBNetwork {
		for _, svc := range []string{"migrate", "admin"} {
			b.WriteString("  " + svc + ":\n    networks:\n" + overlayDBJoin)
		}
	}
	if o.Any() {
		b.WriteString("  serve:\n")
		if o.ProxyLabels {
			b.WriteString(overlayServeLabels)
		}
		if o.ProxyLink || o.DBNetwork {
			b.WriteString("    networks:\n")
			if o.ProxyLink {
				b.WriteString(overlayServeLink)
			}
			if o.DBNetwork {
				b.WriteString(overlayDBJoin)
			}
		}
	}
	if staticTLSProxy != "" {
		b.WriteString(staticTLSBlock(staticTLSProxy))
	}
	if o.DBNetwork {
		b.WriteString("networks:\n" + overlayDBNetworks)
	}
	return b.String()
}

// staticTLSBlock mounts the operator's certificate directory at /certs
// in the bundled proxy (and keeps certbot off for nginx).
func staticTLSBlock(p Proxy) string {
	var b strings.Builder
	b.WriteString(`  # PROXY_TLS_MODE=static: put the certificate chain and key into
  # ./` + ProxyTLSDir + `/tls.crt and ./` + ProxyTLSDir + `/tls.key (readable by UID 65532; key mode
  # 0600, owner 65532). docs/installation/reverse-proxy.md
`)
	b.WriteString("  " + string(p) + ":\n")
	b.WriteString(`    volumes:
      - type: bind
        source: ./` + ProxyTLSDir + `
        target: /certs
        read_only: true
        bind:
          create_host_path: false
`)
	if p == ProxyNginx {
		b.WriteString(`  # No ACME with a static certificate: certbot never starts.
  certbot:
    profiles: !override [static-tls-disabled]
`)
	}
	return b.String()
}

// expectedOverlay is the overlay part of the lint's expected project (no
// static TLS: operator-tunable).
func expectedOverlay(o Overlays) []composeSrc {
	doc := overlayDoc(o, "")
	if doc == "" {
		return nil
	}
	return []composeSrc{{name: OverrideFile, data: []byte(doc)}}
}

// checkSetupFirstOverride requires an existing compose.override.yaml of
// a setup-first deployment to carry the setup-only overlays ov on top of
// the generated compose (D-215): setup never rewrites an existing
// override (init, also with --force, and upgrade), so the blocks must be
// in it already. It runs the lint setup check runs; an error names the
// blocks to add.
func checkSetupFirstOverride(root string, compose, override []byte, v Variant, img imageref.Ref, ov Overlays, lookup func(string) (string, bool)) error {
	if !ov.Any() {
		return nil
	}
	if override == nil {
		override = []byte{}
	}
	if err := singleDocument(OverrideFile, override); err != nil {
		return err
	}
	dotenv, err := readDotEnv(root)
	if err != nil {
		return err
	}
	in := composeInput{root: root, compose: compose, override: override, dotenv: dotenv, lookup: lookup, following: img.IsMutable()}
	env := projectEnv(in.dotenv, in.lookup, in.docs()...)
	var b strings.Builder
	for _, f := range lintComposeFiles(&in, env, v, img.String(), expectedDocs(compose, ov), false) {
		if f.Severity == SeverityError {
			b.WriteString("\n  " + f.String())
		}
	}
	if b.Len() == 0 {
		return nil
	}
	return fmt.Errorf("%s with the existing %s fails the hardening lint:%s\n%s exists already, so setup does not rewrite it: add these blocks to it (merged into its own services: and networks: keys), then run setup again:\n%s",
		ComposeFile, OverrideFile, b.String(), OverrideFile, overlayDoc(ov, ""))
}
