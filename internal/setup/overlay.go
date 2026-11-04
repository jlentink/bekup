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
	// DBOnProxy (with DBNetwork, own-proxy join mode): that network is the
	// proxy's own network (D-216). serve is on it already, so only migrate
	// and admin join the proxy network; there is no database network.
	DBOnProxy bool
	// ExternalAPI: the opt-in external API is enabled (D-220 amendment 4).
	// Not an overlay of its own: it adds the external-api service to the
	// DB-network overlay (unless that network is the proxy's, which it is
	// on already) and to the Traefik labels overlay (the /api/v1/ route).
	ExternalAPI bool
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

// overlayExternalAPILabels is the Traefik label block on the external API
// (D-220 amendment 4): the /api/v1/ route above serve's router, request
// bodies at most 16 KiB (the API accepts an empty body or {}).
const overlayExternalAPILabels = `    # The /api/v1/ route of the external API for YOUR Traefik (D-220): above
    # serve's router, request bodies at most 16 KiB.
    labels:
      traefik.enable: "true"
      traefik.docker.network: ${BEKUP_PROXY_NETWORK:-bekup-proxy}
      traefik.http.routers.bekup-api.rule: "Host(` + "`${BEKUP_DOMAIN:-bekup.invalid}`" + `) && PathPrefix(` + "`/api/v1/`" + `)"
      traefik.http.routers.bekup-api.priority: "400"
      traefik.http.routers.bekup-api.entrypoints: ${TRAEFIK_ENTRYPOINT:-websecure}
      traefik.http.routers.bekup-api.tls: "true"
      traefik.http.routers.bekup-api.tls.certresolver: ${TRAEFIK_CERTRESOLVER:-letsencrypt}
      traefik.http.routers.bekup-api.service: bekup-api
      traefik.http.routers.bekup-api.middlewares: bekup-api-body
      traefik.http.middlewares.bekup-api-body.buffering.maxRequestBodyBytes: "16384"
      traefik.http.services.bekup-api.loadbalancer.server.port: "8081"
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

// overlayDBOnProxy joins a service to the proxy's network, where the
// database container is too (D-216).
const overlayDBOnProxy = `      # YOUR proxy's network (BEKUP_PROXY_NETWORK), where your database
      # container is too (D-216).
      proxy: {}
`

// overlayBackendInternal keeps backend internal: the app database is a
// container on the operator's Docker network.
const overlayBackendInternal = `  # The app database is a container on YOUR Docker network: backend needs no
  # route off this host and stays internal.
  backend:
    internal: true
`

// overlayDBNetworks are the top-level networks of the DB-network overlay.
const overlayDBNetworks = overlayBackendInternal + `  # YOUR database container's network (external; setup writes its name to
  # .env as BEKUP_DB_NETWORK). Only migrate, serve, admin, the browser
  # setup's database run (setup-web-db) and the external API join it.
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

// dbNetworkServices join the database container's network next to serve
// (setup-web-db: the browser setup's database run, D-217).
var dbNetworkServices = []string{"migrate", "admin", browserDBService}

// browserDBService is the browser setup's database run (D-217).
const browserDBService = "setup-web-db"

// overlayDoc renders the overlay blocks as a compose document (services
// and networks only; "" without overlays). staticTLS adds the bundled
// proxy's certificate mount (proxy names traefik or nginx): an
// operator-tunable change that is not part of the lint's expected project.
func overlayDoc(o Overlays, staticTLSProxy Proxy) string {
	return renderOverlay(o, staticTLSProxy, dbNetworkServices)
}

// renderOverlay is overlayDoc with the services that join the database
// network (dbServices).
func renderOverlay(o Overlays, staticTLSProxy Proxy, dbServices []string) string {
	if !o.Any() && staticTLSProxy == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("services:\n")
	join := overlayDBJoin
	if o.DBOnProxy {
		join = overlayDBOnProxy
	}
	if o.DBNetwork {
		for _, svc := range dbServices {
			b.WriteString("  " + svc + ":\n    networks:\n" + join)
		}
	}
	// serve is on the proxy's network already when the database shares it
	// (D-216): a second endpoint would fail.
	serveDB := o.DBNetwork && !o.DBOnProxy
	if o.ProxyLabels || o.ProxyLink || serveDB {
		b.WriteString("  serve:\n")
		if o.ProxyLabels {
			b.WriteString(overlayServeLabels)
		}
		if o.ProxyLink || serveDB {
			b.WriteString("    networks:\n")
			if o.ProxyLink {
				b.WriteString(overlayServeLink)
			}
			if serveDB {
				b.WriteString(overlayDBJoin)
			}
		}
	}
	b.WriteString(externalAPIOverlay(o))
	if staticTLSProxy != "" {
		b.WriteString(staticTLSBlock(staticTLSProxy))
	}
	switch {
	case o.DBOnProxy:
		b.WriteString("networks:\n" + overlayBackendInternal)
	case o.DBNetwork:
		b.WriteString("networks:\n" + overlayDBNetworks)
	}
	return b.String()
}

// externalAPIOverlay is the external-api block of the overlay (D-220
// amendment 4; "" without one): with the external API enabled it reaches
// the database container like serve (not when that network is the
// proxy's: it is on it already) and gets its own route with the Traefik
// labels.
func externalAPIOverlay(o Overlays) string {
	extDB := o.ExternalAPI && o.DBNetwork && !o.DBOnProxy
	extLabels := o.ExternalAPI && o.ProxyLabels
	if !extDB && !extLabels {
		return ""
	}
	var b strings.Builder
	b.WriteString("  external-api:\n")
	if extLabels {
		b.WriteString(overlayExternalAPILabels)
	}
	if extDB {
		b.WriteString("    networks:\n" + overlayDBJoin)
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

// preWebOverlay is the overlay part of the expected project as releases
// before D-217 wrote it: the DB-network overlay without setup-web-db.
func preWebOverlay(o Overlays) []composeSrc {
	var svcs []string
	for _, s := range dbNetworkServices {
		if s != browserDBService {
			svcs = append(svcs, s)
		}
	}
	doc := renderOverlay(o, "", svcs)
	if doc == "" {
		return nil
	}
	return []composeSrc{{name: OverrideFile, data: []byte(doc)}}
}

// lintWithOverlays is the hardening lint against the release file compose
// plus the setup-only overlays ov (expectedDocs). A compose.override.yaml
// written before D-217 carries the DB-network overlay without the
// setup-web-db block: the lint then fails on setup-web-db alone. When the
// project matches the pre-D-217 overlay exactly in that service, the
// missing block is a warning (only the browser setup's database step
// cannot reach the database container; setup never rewrites an existing
// override, D-215), with the block to add.
func lintWithOverlays(in *composeInput, env map[string]string, v Variant, image string, compose []byte, ov Overlays, placeholder bool) []Finding {
	findings := lintComposeFiles(in, env, v, image, expectedDocs(compose, ov), placeholder)
	if !ov.DBNetwork || !blamesService(findings, browserDBService) {
		return findings
	}
	old := lintComposeFiles(in, env, v, image, append([]composeSrc{{name: ComposeFile, data: compose}}, preWebOverlay(ov)...), placeholder)
	if blamesService(old, browserDBService) {
		return findings
	}
	join := overlayDBJoin
	if ov.DBOnProxy {
		join = overlayDBOnProxy
	}
	return append(old, Finding{Severity: SeverityWarning, Path: OverrideFile, Msg: fmt.Sprintf(
		"the browser setup's database run (%s, D-217) is not on your database container's network, so it cannot reach the database (migrate, serve and admin are); "+
			"to use it, add this block to the services: key of %s (setup never rewrites it):\n  %s:\n    networks:\n%s",
		browserDBService, OverrideFile, browserDBService, join)})
}

// blamesService reports a lint error naming the service.
func blamesService(findings []Finding, name string) bool {
	q := fmt.Sprintf("%q", name)
	for _, f := range findings {
		if f.Severity == SeverityError && strings.Contains(f.Msg, q) {
			return true
		}
	}
	return false
}

// checkSetupFirstOverride requires an existing compose.override.yaml of
// a setup-first deployment to carry the setup-only overlays ov on top of
// the generated compose (D-215): setup never rewrites an existing
// override (init, also with --force, and upgrade), so the blocks must be
// in it already. It runs the lint setup check runs; an error names the
// blocks to add.
func checkSetupFirstOverride(root string, compose, override []byte, v Variant, img imageref.Ref, ov Overlays, lookup func(string) (string, bool)) ([]string, error) {
	if !ov.Any() {
		return nil, nil
	}
	if override == nil {
		override = []byte{}
	}
	if err := singleDocument(OverrideFile, override); err != nil {
		return nil, err
	}
	dotenv, err := readDotEnv(root)
	if err != nil {
		return nil, err
	}
	in := composeInput{root: root, compose: compose, override: override, dotenv: dotenv, lookup: lookup, following: img.IsMutable()}
	env := projectEnv(in.dotenv, in.lookup, in.docs()...)
	var b strings.Builder
	var warnings []string
	for _, f := range lintWithOverlays(&in, env, v, img.String(), compose, ov, false) {
		switch f.Severity {
		case SeverityError:
			b.WriteString("\n  " + f.String())
		case SeverityWarning:
			warnings = append(warnings, f.String())
		}
	}
	if b.Len() == 0 {
		return warnings, nil
	}
	return nil, fmt.Errorf("%s with the existing %s fails the hardening lint:%s\n%s exists already, so setup does not rewrite it: add these blocks to it (merged into its own services: and networks: keys), then run setup again:\n%s",
		ComposeFile, OverrideFile, b.String(), OverrideFile, overlayDoc(ov, ""))
}
