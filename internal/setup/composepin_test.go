package setup

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/jlentink/bekup"
)

// renderedVariant renders a shipped variant with the test image.
func renderedVariant(t testing.TB, v Variant) string {
	t.Helper()
	img := mustRef(t, variantImage)
	b, err := renderEmbedded(bekup.Assets(), v, &img)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestComposeVarAllowlist: the allowlist is exactly the set of variables
// the shipped files use (rendered with a pinned image), and they use no
// other `$`.
func TestComposeVarAllowlist(t *testing.T) {
	got := map[string]bool{}
	for _, v := range AllVariants {
		vars, bad, err := composeVarRefs([]byte(renderedVariant(t, v)), false)
		if err != nil || bad {
			t.Fatalf("%s: %v bad=%v", v, err, bad)
		}
		for k := range vars {
			got[k] = true
		}
		if fs := interpolationFindings(ComposeFile, []byte(renderedVariant(t, v)), false); len(fs) > 0 {
			t.Fatalf("%s: %v", v, fs)
		}
	}
	keys := sortedKeys(got)
	want := slices.Clone(composeVarAllowlist)
	slices.Sort(want)
	if !slices.Equal(keys, want) {
		t.Fatalf("shipped variables %v, allowlist %v", keys, want)
	}
	for _, tc := range []struct {
		in   string
		bad  bool
		vars []string
	}{
		{"a: ${A}\n", false, []string{"A"}},
		{"a: $A-$$B\n", false, []string{"A"}},
		{"a: ${A:-${B}}\n", false, []string{"A", "B"}},
		{"a: $$\n", false, nil},
		{"a: x$\n", true, nil},
		{"a: $(id)\n", true, nil},
		{"# ${COMMENT}\na: 1\n", false, nil},
		{"${KEY}: 1\n", false, []string{"KEY"}},
		{"x: &a ${A}\ny: *a\n", false, []string{"A"}},
		{"a: !!str ${A}\nb: !!int 1\nc: !reset []\nd: !override {}\n", false, []string{"A"}},
	} {
		vars, bad, err := composeVarRefs([]byte(tc.in), false)
		if err != nil || bad != tc.bad || !slices.Equal(sortedKeys(vars), tc.vars) && len(tc.vars)+len(vars) > 0 {
			t.Errorf("%q: %v %v %v", tc.in, sortedKeys(vars), bad, err)
		}
	}
}

// TestComposeYAMLTags: explicit YAML tags other than the core kinds and
// compose's !reset/!override are refused (the variable scan reads node
// text, which is not what compose-go interpolates for !!binary).
func TestComposeYAMLTags(t *testing.T) {
	for _, in := range []string{
		"name: !!binary JHtVU0VSOi1iZWt1cH0=\n",
		"!!binary eA==: 1\n",
		"a: !!set {x: null}\n",
		"a: !!timestamp 2001-12-14\n",
		"a: !custom x\n",
		"%TAG !e! tag:yaml.org,2002:\n---\na: !e!binary eA==\n",
	} {
		_, _, err := composeVarRefs([]byte(in), false)
		var te *yamlTagError
		if !errors.As(err, &te) {
			t.Errorf("%q: %v", in, err)
		}
	}
	for _, in := range []string{"a: 2001-12-14\n", "a: ! x\n", "a: !!str x\n", "<<: {a: 1}\n"} {
		if _, _, err := composeVarRefs([]byte(in), false); err != nil {
			t.Errorf("%q: %v", in, err)
		}
	}
}

// shippedEdit inserts lines after the first occurrence of anchor in the
// rendered variant (compose.yaml case).
func shippedEdit(t testing.TB, v Variant, anchor, add string) string {
	t.Helper()
	s := renderedVariant(t, v)
	if !strings.Contains(s, anchor) {
		t.Fatalf("no %q in %s", anchor, v)
	}
	return strings.Replace(s, anchor, anchor+add, 1)
}

const (
	serveAnchor  = "    command: [serve]\n"
	runnerAnchor = "    command: [runner]\n"
	hardened     = "    user: \"1000:1000\"\n    read_only: true\n    cap_drop: [ALL]\n    security_opt: [\"no-new-privileges:true\"]\n"
)

// addedService is a hardened service the operator adds, with extra lines.
func addedService(extra string) string {
	return "  extra:\n    image: alpine:3@" + testDigest2 + "\n" + hardened + extra
}

// TestStructuralLint: a regression case for every finding of the
// security re-check (H1, H2, M1-M3, L1-L6, info), each in a customized
// compose.yaml and in compose.override.yaml.
func TestStructuralLint(t *testing.T) {
	type tc struct {
		name    string
		v       Variant
		compose string // compose.yaml: lines after anchor ("" = unchanged)
		anchor  string
		top     string // top-level YAML appended to compose.yaml
		over    string // override
		want    string
		sev     Severity
	}
	cases := []tc{
		// H1: host-shell variables.
		{name: "H1 XDG bind", anchor: "services:\n", compose: addedService("    volumes: [\"${XDG_RUNTIME_DIR:-./data}:/x\"]\n"),
			over: "services:\n" + addedService("    volumes: [\"${XDG_RUNTIME_DIR:-./data}:/x\"]\n"), want: "${XDG_RUNTIME_DIR} is not one of bekup's compose variables"},
		{name: "H1 XDG bind uses vars", anchor: "services:\n", compose: addedService("    volumes: [\"${XDG_RUNTIME_DIR:-./data}:/x\"]\n"),
			over: "services:\n" + addedService("    volumes: [\"${XDG_RUNTIME_DIR:-./data}:/x\"]\n"), want: "cannot be checked for ${...} variables"},
		{name: "H1 SSH agent on runner", anchor: runnerAnchor, compose: "    environment: {SSH_AUTH_SOCK: /ssh-agent}\n",
			over: "services:\n  runner:\n    volumes: [\"${SSH_AUTH_SOCK:-./a}:/ssh-agent\"]\n", want: `service "runner"`},
		{name: "H1 allowed var in a new place", anchor: "services:\n", compose: addedService("    volumes: [\"${BEKUP_KEYS_DIR:-./data}:/x\"]\n"),
			over: "services:\n" + addedService("    volumes: [\"${BEKUP_KEYS_DIR:-./data}:/x\"]\n"), want: "cannot be checked for ${...} variables"},
		{name: "H1 allowed var in a long bind", anchor: "services:\n", compose: addedService("    volumes: [{type: bind, source: \"${BEKUP_KEYS_DIR:-./data}\", target: /x}]\n"),
			over: "services:\n" + addedService("    volumes: [{type: bind, source: \"${BEKUP_KEYS_DIR:-./data}\", target: /x}]\n"), want: `added service "extra": uses ${...} variables`},
		{name: "H1 allowed var on a shipped service", anchor: serveAnchor, compose: "    hostname: ${BEKUP_DOMAIN:-x}\n",
			over: "services:\n  serve:\n    hostname: ${BEKUP_DOMAIN:-x}\n", want: "hostname use ${...} variables where the shipped file does not"},
		{name: "H1 runner socket var", anchor: "services:\n", compose: "",
			over: "services:\n  runner:\n    volumes: !override [{type: bind, source: \"/run/user/${DOCKER_GID:-1000}/docker.sock\", target: /var/run/docker.sock, read_only: true}]\n", want: "volumes use ${...} variables"},
		// H2: environment/command/entrypoint on bekup services.
		{name: "H2 BEKUP_DEV", anchor: serveAnchor, compose: "    environment: {BEKUP_DEV: \"1\"}\n",
			over: "services:\n  serve:\n    environment: {BEKUP_DEV: \"1\"}\n", want: `"serve"`},
		{name: "H2 allow cidrs", anchor: runnerAnchor, compose: "    environment: {BEKUP_TARGETS_ALLOW_CIDRS: 0.0.0.0/0}\n",
			over: "services:\n  runner:\n    environment: {BEKUP_TARGETS_ALLOW_CIDRS: 0.0.0.0/0}\n", want: "in: environment"},
		{name: "H2 client CA", anchor: serveAnchor, compose: "    environment: {BEKUP_TLS_CLIENT_CA_FILE: /tmp/ca}\n",
			over: "services:\n  serve:\n    environment: {BEKUP_TLS_CLIENT_CA_FILE: /tmp/ca}\n", want: "environment"},
		{name: "H2 images extra", anchor: runnerAnchor, compose: "    environment: {BEKUP_IMAGES_EXTRA_FILE: /tmp/x}\n",
			over: "services:\n  runner:\n    environment: {BEKUP_IMAGES_EXTRA_FILE: /tmp/x}\n", want: "environment"},
		{name: "H2 --dev", anchor: "", over: "services:\n  serve:\n    command: [serve, --dev]\n", want: "in: command"},
		{name: "H2 entrypoint", anchor: "", over: "services:\n  migrate:\n    entrypoint: [/bin/sh]\n", want: "in: entrypoint"},
		{name: "H2 postgres env", anchor: "", over: "services:\n  postgres:\n    environment: {POSTGRES_HOST_AUTH_METHOD: trust}\n", want: `"postgres"`},
		{name: "H2 traefik env", anchor: "", over: "services:\n  traefik:\n    environment: {ACME_CA_SERVER: http://evil}\n", want: "ACME_CA_SERVER may not be set or changed"},
		// M1: bekup's files, volumes, secrets.
		{name: "M1 secrets dir", anchor: "services:\n", compose: addedService("    volumes: [\"./secrets:/s:ro\"]\n"), over: "services:\n" + addedService("    volumes: [\"./secrets:/s:ro\"]\n"), want: "may not mount ./secrets"},
		{name: "M1 secrets file", anchor: "services:\n", compose: addedService("    volumes: [\"./secrets/bekup_master_key:/k:ro\"]\n"), over: "services:\n" + addedService("    volumes: [\"./secrets/bekup_master_key:/k:ro\"]\n"), want: "may not mount ./secrets/bekup_master_key"},
		{name: "M1 secrets case", over: "services:\n" + addedService("    volumes: [\"./SECRETS:/s:ro\"]\n"), want: "may not mount ./SECRETS"},
		{name: "M1 keys dir from env", over: "services:\n" + addedService("    volumes: [\"./vault/k:/s:ro\"]\n"), want: "may not mount ./vault/k"},
		{name: "M1 toml", over: "services:\n" + addedService("    volumes: [\"./bekup.toml:/c:ro\"]\n"), want: "may not mount ./bekup.toml"},
		{name: "M1 env", over: "services:\n" + addedService("    volumes: [\"./.env:/c:ro\"]\n"), want: "may not mount ./.env"},
		{name: "M1 state", over: "services:\n" + addedService("    volumes: [\"./bekup-setup.json:/c\"]\n"), want: "may not mount ./bekup-setup.json"},
		{name: "M1 compose", over: "services:\n" + addedService("    volumes: [\"./compose.yaml:/c\"]\n"), want: "may not mount ./compose.yaml"},
		{name: "M1 deploy", over: "services:\n" + addedService("    volumes: [\"./deploy/postgres:/c\"]\n"), want: "may not mount ./deploy/postgres"},
		{name: "M1 env_file", over: "services:\n" + addedService("    env_file: [./secrets/bekup_master_key]\n"), want: "env_file ./secrets/bekup_master_key"},
		{name: "M1 postgres-data", anchor: "services:\n", compose: addedService("    volumes: [\"postgres-data:/d\"]\n"), over: "services:\n" + addedService("    volumes: [\"postgres-data:/d\"]\n"), want: `may not mount bekup's volume "postgres-data"`},
		{name: "M1 traefik-acme", over: "services:\n" + addedService("    volumes: [\"traefik-acme:/d:ro\"]\n"), want: `bekup's volume "traefik-acme"`},
		{name: "M1 shipped secret", over: "services:\n" + addedService("    secrets: [bekup_master_key]\n"), want: `may not use bekup's secret "bekup_master_key"`},
		{name: "M1 shipped config", over: "services:\n" + addedService("    configs: [runner_toml]\n"), want: `may not use bekup's config "runner_toml"`},
		{name: "M1 volume alias", top: "  foreign:\n    name: otherproject_postgres-data\n", anchor: "volumes:\n",
			over: "volumes:\n  foreign:\n    name: otherproject_postgres-data\n", want: "is not allowed on an added volume"},
		{name: "M1 shipped volume name", over: "volumes:\n  postgres-data:\n    name: other_pg\n", want: `volume "postgres-data" differs`},
		{name: "M1 project name", over: "name: otherproject\n", want: "project name"},
		{name: "M1 secret environment", top: "  hostenv:\n    environment: HOME\n", anchor: "secrets:\n",
			over: "secrets:\n  hostenv:\n    environment: HOME\n", want: "`environment:` is not allowed"},
		{name: "M1 config environment", over: "configs:\n  hostenv:\n    environment: PATH\n", want: "`environment:` is not allowed"},
		{name: "M1 added secret from keys", over: "secrets:\n  k:\n    file: ./secrets/bekup_master_key\n", want: `secret "k": ./secrets/bekup_master_key`},
		{name: "M1 shipped secret file", over: "secrets:\n  bekup_master_key:\n    file: ./other\n", want: `secret "bekup_master_key" differs`},
		// M2: networks.
		{name: "M2 internal false", anchor: "", over: "networks:\n  runner-ctl:\n    internal: false\n", want: `network "runner-ctl" differs`},
		{name: "M2 subnet", anchor: "", over: "networks:\n  frontend:\n    ipam:\n      config: !override [{subnet: 10.9.0.0/16}]\n", want: `network "frontend" differs`},
		{name: "M2 external bridge", over: "networks:\n  br:\n    external: true\n    name: bridge\n", want: `network "br": external networks are not allowed`},
		{name: "M2 serve on bridge", over: "services:\n  serve:\n    networks: [br]\nnetworks:\n  br: {}\n", want: `"serve"`},
		{name: "M2 serve aliases", over: "services:\n  serve:\n    networks:\n      backend:\n        aliases: [postgres2]\n", want: "in: networks"},
		{name: "M2 runner extra_hosts", anchor: runnerAnchor, compose: "    extra_hosts: [\"db:10.0.0.1\"]\n", over: "services:\n  runner:\n    extra_hosts: [\"db:10.0.0.1\"]\n", want: "extra_hosts"},
		{name: "M2 serve dns", over: "services:\n  serve:\n    dns: [10.0.0.53]\n", want: "dns"},
		{name: "M2 serve external_links", over: "services:\n  serve:\n    external_links: [evil]\n", want: "external_links"},
		{name: "M2 serve links", over: "services:\n  serve:\n    links: [postgres]\n", want: "links"},
		{name: "M2 network_mode on serve", over: "services:\n  serve:\n    network_mode: none\n    networks: !reset {}\n", want: "network_mode"},
		{name: "M2 added on backend", anchor: "services:\n", compose: addedService("    networks: [backend]\n"), over: "services:\n" + addedService("    networks: [backend]\n"), want: `may not join bekup's network "backend"`},
		{name: "M2 added on runner-ctl", over: "services:\n" + addedService("    networks: [runner-ctl]\n"), want: `bekup's network "runner-ctl"`},
		{name: "M2 added on frontend", over: "services:\n" + addedService("    networks: [frontend]\n"), want: `bekup's network "frontend"`},
		{name: "M2 added on proxy link", v: VariantNPM, over: "services:\n" + addedService("    networks: [proxy]\n"), want: `bekup's network "proxy"`},
		{name: "M2 added alias on metrics", over: "services:\n" + addedService("    networks:\n      metrics:\n        aliases: [serve]\n"), want: "network aliases"},
		{name: "M2 added external_links", over: "services:\n" + addedService("    external_links: [x]\n"), want: "`external_links`"},
		{name: "M2 added container_name", over: "services:\n" + addedService("    container_name: serve\n    networks: [metrics]\n"), want: "`container_name`"},
		// L1: absolute paths naming the deployment directory stay outside.
		{name: "L1 /deploy", anchor: "services:\n", compose: addedService("    volumes: [\"/deploy/x:/x\"]\n"), over: "services:\n" + addedService("    volumes: [\"/deploy/x:/x\"]\n"), want: "from outside the deployment directory"},
		{name: "L1 /deploy/..", over: "services:\n" + addedService("    volumes: [\"/deploy/../deploy/x:/x\"]\n"), want: "from outside the deployment directory"},
		{name: "L1 secret /deploy", over: "secrets:\n  s:\n    file: /deploy/x\n", want: "outside the deployment directory"},
		// L3: logging.
		{name: "L3 gelf", anchor: "services:\n", compose: addedService("    logging: {driver: gelf, options: {gelf-address: \"udp://1.2.3.4:12201\"}}\n"), over: "services:\n" + addedService("    logging: {driver: gelf, options: {gelf-address: \"udp://1.2.3.4:12201\"}}\n"), want: `logging driver "gelf"`},
		{name: "L3 option", over: "services:\n" + addedService("    logging: {driver: json-file, options: {labels-regex: x, tag: y}}\n"), want: `logging option "labels-regex"`},
		{name: "L3 serve logging", over: "services:\n  serve:\n    logging: {driver: syslog}\n", want: "logging"},
		// L4: group_add.
		{name: "L4 added", anchor: "services:\n", compose: addedService("    group_add: [\"999\"]\n"), over: "services:\n" + addedService("    group_add: [\"999\"]\n"), want: "`group_add` is not allowed"},
		{name: "L4 serve", over: "services:\n  serve:\n    group_add: [\"999\"]\n", want: "group_add"},
		{name: "L4 runner other gid", over: "services:\n  runner:\n    group_add: !override [\"0\"]\n", want: "group_add"},
		// L5: host DoS keys.
		{name: "L5 oom_score_adj", anchor: "services:\n", compose: addedService("    oom_score_adj: -1000\n"), over: "services:\n" + addedService("    oom_score_adj: -1000\n"), want: "negative oom_score_adj"},
		{name: "L5 oom_kill_disable", over: "services:\n" + addedService("    oom_kill_disable: true\n"), want: "`oom_kill_disable`"},
		{name: "L5 memlock", over: "services:\n" + addedService("    ulimits: {memlock: -1}\n"), want: `ulimit "memlock"`},
		{name: "L5 nofile", over: "services:\n" + addedService("    ulimits: {nofile: {soft: 1024, hard: 99999999}}\n"), want: `ulimit "nofile" must be`},
		{name: "L5 shm", over: "services:\n" + addedService("    shm_size: 64g\n"), want: "shm_size above 1 GiB"},
		{name: "L5 rt", over: "services:\n" + addedService("    cpu_rt_runtime: 950000\n"), want: "real-time CPU"},
		{name: "L5 pids", over: "services:\n" + addedService("    pids_limit: -1\n"), want: "unlimited pids_limit"},
		{name: "L5 serve oom", over: "services:\n  serve:\n    oom_score_adj: -999\n", want: "oom_score_adj"},
		{name: "L5 serve ulimits", over: "services:\n  serve:\n    ulimits: {nproc: 999999}\n", want: "ulimits"},
		{name: "L5 serve deploy pids", over: "services:\n  serve:\n    deploy: {resources: {limits: {pids: -1}}}\n", want: "unlimited pids"},
		// L6: labels.
		{name: "L6 traefik label on added", anchor: "services:\n", compose: addedService("    labels: {traefik.http.routers.x.rule: \"Host(`bekup.example.com`)\"}\n"), over: "services:\n" + addedService("    labels: {traefik.http.routers.x.rule: \"Host(`bekup.example.com`)\"}\n"), want: `label "traefik.http.routers.x.rule"`},
		{name: "L6 deploy label", over: "services:\n" + addedService("    deploy: {labels: {Traefik.enable: \"true\"}}\n"), want: `label "Traefik.enable"`},
		{name: "L6 serve label", v: VariantTraefikExternal, over: "services:\n  serve:\n    labels: {traefik.http.routers.bekup.middlewares: evil}\n", want: "labels"},
		{name: "L6 postgres label", over: "services:\n  postgres:\n    labels: {traefik.enable: \"true\"}\n", want: "labels"},
		// Info: userns_mode and SELinux.
		{name: "userns serve", anchor: serveAnchor, compose: "    userns_mode: host\n", over: "services:\n  serve:\n    userns_mode: host\n", want: "`userns_mode: host` is allowed only on the runner"},
		{name: "userns added", over: "services:\n" + addedService("    userns_mode: host\n"), want: "allowed only on the runner"},
		{name: "selinux runner socket", over: "services:\n  runner:\n    volumes: !override [\"/var/run/docker.sock:/var/run/docker.sock:ro,Z\"]\n", want: "no SELinux relabeling"},
		{name: "selinux added", over: "services:\n" + addedService("    volumes: [\"./data:/d:z\"]\n"), want: "SELinux relabeling"},
		// Shipped services stay complete.
		{name: "serve privileged", over: "services:\n  serve:\n    privileged: true\n", want: "privileged"},
		{name: "serve hostname", over: "services:\n  serve:\n    hostname: x\n", want: "in: hostname"},
		{name: "runner extra mount", over: "services:\n  runner:\n    volumes: [\"./deploy:/d:ro\"]\n", want: "nothing but the Docker socket"},
		{name: "certs-init profile", over: "services:\n  certs-init:\n    profiles: !override [x]\n", want: "profiles"},
		{name: "x keys", over: "services:\n  serve:\n    x-evil: 1\n", want: "x-* keys"},
		// Re-check findings: the project name and YAML tags.
		{name: "name binary", over: "name: !!binary JHtVU0VSOi1iZWt1cH0=\n", want: "the YAML tag !!binary is not allowed"},
		{name: "name long binary tag", over: "name: !<tag:yaml.org,2002:binary> JHtVU0VSOi1iZWt1cH0=\n", want: "the YAML tag !!binary is not allowed"},
		{name: "name via allowed var", over: "name: ${ACME_CA_SERVER:-bekup}\n", want: "the project name is written differently"},
		{name: "binary in serve", anchor: serveAnchor, compose: "    hostname: !!binary JHtVU0VSfQ==\n", over: "services:\n  serve:\n    hostname: !!binary JHtVU0VSfQ==\n", want: "the YAML tag !!binary is not allowed"},
		{name: "binary in added", over: "services:\n" + addedService("    working_dir: !!binary JHtIT01FfQ==\n"), want: "the YAML tag !!binary is not allowed"},
		{name: "custom tag", over: "services:\n" + addedService("    working_dir: !env HOME\n"), want: "the YAML tag !env is not allowed"},
		// Re-check findings: added services taking bekup's addresses.
		{name: "added metrics serve ip", over: "services:\n" + addedService("    networks:\n      metrics:\n        ipv4_address: 10.213.252.10\n"), want: `fixed addresses (ipv4_address, ipv6_address, mac_address, link_local_ips) on bekup's network "metrics"`},
		{name: "added metrics other ip", over: "services:\n" + addedService("    networks:\n      metrics:\n        ipv4_address: 10.213.252.20\n"), want: `on bekup's network "metrics" are not allowed`},
		{name: "added metrics mac", over: "services:\n" + addedService("    networks:\n      metrics:\n        mac_address: 02:42:ac:11:00:02\n"), want: `on bekup's network "metrics" are not allowed`},
		{name: "added service mac", over: "services:\n" + addedService("    mac_address: 02:42:ac:11:00:02\n    networks: [metrics]\n"), want: "`mac_address` is not allowed"},
		{name: "added frontend proxy ip", v: VariantFull, over: "services:\n" + addedService("    networks:\n      frontend:\n        ipv4_address: 10.213.250.5\n"), want: `the address 10.213.250.5 on bekup's network "frontend" belongs to a bekup service`},
		{name: "added frontend serve ip", v: VariantFull, over: "services:\n" + addedService("    networks:\n      frontend:\n        ipv4_address: 10.213.250.10\n"), want: "belongs to a bekup service"},
		{name: "added frontend link local", v: VariantFull, over: "services:\n" + addedService("    networks:\n      frontend:\n        link_local_ips: [169.254.1.1]\n"), want: "link_local_ips"},
		// Re-check findings: resource limits stay.
		{name: "deploy reset", anchor: "", over: "services:\n  serve:\n    deploy: !reset null\n", want: "the shipped deploy.resources.limits cpus, memory, pids must stay set"},
		{name: "deploy limits reset", over: "services:\n  runner:\n    deploy:\n      resources:\n        limits: !reset {}\n", want: "must stay set"},
		{name: "deploy pids 0", over: "services:\n  serve:\n    deploy: {resources: {limits: {pids: 0}}}\n", want: "limits pids must stay set"},
		{name: "pids_limit 0", anchor: serveAnchor, compose: "    pids_limit: 0\n", over: "services:\n  serve:\n    pids_limit: 0\n", want: `service "serve": pids_limit must be above 0`},
		{name: "pids_limit 0 added", over: "services:\n" + addedService("    pids_limit: \"0\"\n"), want: `service "extra": pids_limit must be above 0`},
		{name: "pids_limit 0 merge", over: "x-l: &l {pids_limit: 0}\nservices:\n  postgres:\n    <<: *l\n", want: `service "postgres": pids_limit must be above 0`},
	}
	env := map[string]string{"BEKUP_KEYS_DIR": "./vault/k"}
	for _, c := range cases {
		v := c.v
		if v == "" {
			v = VariantTraefik
		}
		sev := c.sev
		if sev == "" {
			sev = SeverityError
		}
		if c.over != "" {
			t.Run(c.name+"/override", func(t *testing.T) {
				fs := lintWith(t, nil, []byte(c.over), v, env)
				if !hasFinding(fs, sev, c.want) {
					t.Fatalf("no %s %q in %v", sev, c.want, fs)
				}
			})
		}
		if c.compose == "" && c.top == "" {
			continue
		}
		t.Run(c.name+"/compose.yaml", func(t *testing.T) {
			var s string
			switch {
			case c.top != "":
				s = shippedEdit(t, v, "\n"+c.anchor, c.top)
			default:
				s = shippedEdit(t, v, c.anchor, c.compose)
			}
			fs := lintWith(t, []byte(s), nil, v, env)
			if !hasFinding(fs, sev, c.want) {
				t.Fatalf("no %s %q in %v", sev, c.want, fs)
			}
		})
	}
}

// TestOperatorTunables: the documented overrides and the operator-tunable
// attributes pass; anything beyond them is an error.
func TestOperatorTunables(t *testing.T) {
	staticTraefik := renderOverride(&Answers{Proxy: ProxyTraefik})
	staticNginx := renderOverride(&Answers{Proxy: ProxyNginx})
	for _, tc := range []struct {
		name, over string
		v          Variant
		warn       string
	}{
		{name: "empty"},
		{name: "combined documented overrides", warn: "userns_mode: host", over: "services:\n  traefik:\n    environment:\n      PROXY_TLS_MODE: static\n      PROXY_HTTPS_PUBLIC_PORT: \"8443\"\n    ports: !override [\"127.0.0.1:8443:8443\"]\n    volumes:\n      - type: bind\n        source: ./proxy-tls\n        target: /certs\n        read_only: true\n  runner:\n    userns_mode: host\n    volumes:\n      - /run/user/1000/docker.sock:/var/run/docker.sock:ro\n  serve:\n    volumes:\n      - ./backups:/var/lib/bekup/backups/disk\n"},
		{name: "rootless socket (rootless-docker.md)", over: "services:\n  runner:\n    volumes:\n      - /run/user/1000/docker.sock:/var/run/docker.sock:ro\n"},
		{name: "rootless socket long", over: "services:\n  runner:\n    volumes: !override\n      - type: bind\n        source: /run/user/1000/docker.sock\n        target: /var/run/docker.sock\n        read_only: true\n        bind: {create_host_path: false}\n"},
		{name: "userns runner (rootless-docker.md)", over: "services:\n  runner:\n    userns_mode: host\n", warn: "userns_mode: host"},
		{name: "prometheus (operations.md)", over: "services:\n  prometheus:\n    image: prom/prometheus:v3@" + testDigest2 + "\n    user: \"65534:65534\"\n    read_only: true\n    cap_drop: [ALL]\n    security_opt: [\"no-new-privileges:true\"]\n    volumes:\n      - ./prometheus.yml:/etc/prometheus/prometheus.yml:ro\n      - prometheus-data:/prometheus\n    networks: [metrics]\nvolumes:\n  prometheus-data: {}\n"},
		{name: "static TLS (reverse-proxy.md)", over: "services:\n  traefik:\n    environment:\n      PROXY_TLS_MODE: static\n    volumes:\n      - type: bind\n        source: ./proxy-tls\n        target: /certs\n        read_only: true\n"},
		{name: "static TLS written by setup (traefik)", over: string(staticTraefik)},
		{name: "static TLS written by setup (nginx)", over: string(staticNginx), v: VariantFull},
		{name: "ports (reverse-proxy.md)", over: "services:\n  traefik:\n    ports: !override [\"127.0.0.1:8443:8443\"]\n    environment:\n      PROXY_HTTPS_PUBLIC_PORT: \"8443\"\n"},
		{name: "resources", over: "services:\n  serve:\n    deploy:\n      resources:\n        limits: {cpus: \"4\", memory: 4G, pids: 1024}\n        reservations: {memory: 1G}\n  postgres:\n    deploy: {resources: {limits: {memory: 8G}}}\n    restart: always\n    stop_grace_period: 120s\n  runner:\n    mem_reservation: 128m\n"},
		{name: "disk destination bind", over: "services:\n  serve:\n    volumes:\n      - ./backups:/var/lib/bekup/backups\n      - ./disk2:/var/lib/bekup/backups/disk2\n"},
		{name: "disk destination volume", over: "services:\n  serve:\n    volumes: [\"bigdisk:/var/lib/bekup/backups/big\"]\nvolumes:\n  bigdisk: {}\n"},
		{name: "own network for added services", over: "services:\n" + addedService("    networks: [mine]\n") + "networks:\n  mine:\n    internal: true\n"},
		{name: "added default network", over: "services:\n" + addedService("    logging: {driver: local, options: {max-size: 10m}}\n    ulimits: {nofile: 65536}\n")},
		{name: "own proxy on frontend (full, proxy none)", v: VariantFull, over: "services:\n  myproxy:\n    image: caddy:2@" + testDigest2 + "\n" + hardened + "    networks:\n      frontend:\n        ipv4_address: 10.213.250.6\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.v
			if v == "" {
				v = VariantTraefik
			}
			fs := lintWith(t, nil, []byte(tc.over), v, nil)
			if hasErrors(fs) {
				t.Fatalf("%v", fs)
			}
			if tc.warn != "" && !hasFinding(fs, SeverityWarning, tc.warn) {
				t.Fatalf("no warning %q in %v", tc.warn, fs)
			}
			if tc.warn == "" && len(fs) > 0 {
				t.Fatalf("findings: %v", fs)
			}
		})
	}
	for _, tc := range []struct{ name, over, want string }{
		{"deploy replicas", "services:\n  serve:\n    deploy: {replicas: 2}\n", "only the cpus, memory and pids"},
		{"deploy restart policy", "services:\n  serve:\n    deploy: {restart_policy: {condition: any}}\n", "deploy"},
		{"restart bogus", "services:\n  serve:\n    restart: sometimes\n", "restart"},
		{"setup restart", "services:\n  setup:\n    restart: always\n", "the override changes the setup service"},
		{"setup limits", "services:\n  setup:\n    deploy: {resources: {limits: {memory: 4G}}}\n", "the override changes the setup service"},
		{"tls mode bogus", "services:\n  traefik:\n    environment: {PROXY_TLS_MODE: off}\n", "PROXY_TLS_MODE must be acme or static"},
		{"public port bogus", "services:\n  traefik:\n    environment: {PROXY_HTTPS_PUBLIC_PORT: \"99999\"}\n", "PROXY_HTTPS_PUBLIC_PORT"},
		{"certs from secrets", "services:\n  traefik:\n    volumes: [\"./secrets:/certs:ro\"]\n", "/certs must be a read-only bind of ./proxy-tls"},
		{"certs writable", "services:\n  traefik:\n    volumes: [\"./proxy-tls:/certs\"]\n", "/certs must be a read-only bind"},
		{"traefik other mount", "services:\n  traefik:\n    volumes: [\"./proxy-tls:/etc/traefik/dynamic:ro\"]\n", "only a read-only ./proxy-tls bind at /certs"},
		{"traefik acme replaced", "services:\n  traefik:\n    volumes: [\"./acme:/acme\"]\n", "the mount at /acme may not change"},
		{"ports other target", "services:\n  traefik:\n    ports: !override [\"127.0.0.1:9000:9000\"]\n", "shipped container ports"},
		{"ports udp", "services:\n  traefik:\n    ports: !override [\"443:8443/udp\"]\n", "shipped container ports"},
		{"serve bind secrets", "services:\n  serve:\n    volumes: [\"./secrets:/var/lib/bekup/backups/x\"]\n", "holds bekup's own files"},
		{"serve bind outside target", "services:\n  serve:\n    volumes: [\"./data:/etc/bekup/x\"]\n", "below /var/lib/bekup/backups/"},
		{"serve volume postgres", "services:\n  serve:\n    volumes: [\"postgres-data:/var/lib/bekup/backups\"]\n", "postgres-data belongs to another bekup service"},
		{"serve tmpfs", "services:\n  serve:\n    volumes: [{type: tmpfs, target: /var/lib/bekup/backups/t}]\n", "tmpfs mount is not allowed on serve"},
		{"serve host bind", "services:\n  serve:\n    volumes: [\"/mnt/disk:/var/lib/bekup/backups/d\"]\n", "from inside the deployment directory"},
		{"runner socket elsewhere", "services:\n  runner:\n    volumes: !override [\"/tmp/docker.sock:/var/run/docker.sock:ro\"]\n", "the Docker socket must be"},
		{"runner socket rw", "services:\n  runner:\n    volumes: !override [\"/run/docker.sock:/var/run/docker.sock\"]\n", "the Docker socket must be"},
		{"runner userns other", "services:\n  runner:\n    userns_mode: \"ns:/proc/1/ns/user\"\n", "only `userns_mode: host`"},
		{"profiles on serve", "services:\n  serve:\n    profiles: [off]\n", "profiles"},
	} {
		t.Run("deny/"+tc.name, func(t *testing.T) {
			if fs := lintWith(t, nil, []byte(tc.over), VariantTraefik, nil); !hasFinding(fs, SeverityError, tc.want) {
				t.Fatalf("no error %q in %v", tc.want, fs)
			}
		})
	}
}

// TestShippedVariantsPass: every shipped variant (release and full) lints
// clean, also with non-default .env values.
func TestShippedVariantsPass(t *testing.T) {
	env := map[string]string{
		"SERVE_PROXY_IP": "10.213.253.11", "RUNNER_CTL_SUBNET": "10.9.0.0/24", "RUNNER_CTL_IP": "10.9.0.10",
		"DOCKER_GID": "998", "BEKUP_DOMAIN": "b.example.com", "BEKUP_KEYS_DIR": "./keys",
	}
	for _, v := range AllVariants {
		for _, e := range []map[string]string{nil, env} {
			if fs := lintWith(t, nil, nil, v, e); len(fs) > 0 {
				t.Errorf("%s (%v): %v", v, e, fs)
			}
		}
	}
}

// TestLintSymlinkedBindSource: init and upgrade lint without check's
// symlink walk, so the lint itself refuses a symlink on the way to a bind
// source, env_file or secret file (M3).
func TestLintSymlinkedBindSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink("/", filepath.Join(dir, "data")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "conf"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "conf", "env")); err != nil {
		t.Fatal(err)
	}
	img := mustRef(t, variantImage)
	expected, err := renderEmbedded(bekup.Assets(), VariantTraefik, &img)
	if err != nil {
		t.Fatal(err)
	}
	for _, over := range []string{
		"services:\n" + addedService("    volumes: [\"./data:/host:ro\"]\n"),
		"services:\n" + addedService("    volumes: [\"./data/etc:/host:ro\"]\n"),
		"services:\n" + addedService("    env_file: [./conf/env]\n"),
		"secrets:\n  s:\n    file: ./data/etc/shadow\n",
		"services:\n  serve:\n    volumes: [\"./data:/var/lib/bekup/backups/d\"]\n",
	} {
		in := composeInput{root: dir, compose: expected, override: []byte(over)}
		fs := lintComposeFiles(&in, projectEnv(nil, nil, in.docs()...), VariantTraefik, variantImage, expected, false)
		if !hasFinding(fs, SeverityError, "is a symlink") {
			t.Errorf("%q: %v", over, fs)
		}
	}
}

// TestOperatorTunableKeys: every operator-tunable key is a compose-go
// service attribute (a renamed attribute must not silently drop out of
// the comparison), setup has none, and the comparison covers every
// attribute of the pinned compose-go version.
func TestOperatorTunableKeys(t *testing.T) {
	if len(operatorTunable("setup")) > 0 {
		t.Fatal("setup must have no tunables")
	}
	var s types.ServiceConfig
	keys := map[string]bool{}
	for _, f := range serviceFields(&s) {
		keys[f.key] = true
	}
	for k := range reviewedServiceKeys {
		if !keys[k] {
			t.Errorf("reviewed key %q is not compared", k)
		}
	}
	for _, name := range []string{"serve", "runner", "postgres", "migrate", "admin", "certs-init", "traefik", "traefik-init", "nginx", "nginx-init", "certbot"} {
		for k := range operatorTunable(name) {
			if !keys[k] {
				t.Errorf("%s: tunable %q is not a service attribute", name, k)
			}
		}
	}
}
