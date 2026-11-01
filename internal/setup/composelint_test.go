package setup

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/jlentink/bekup"
)

// lintWith lints compose (nil: the shipped variant v) plus override (nil:
// none) resolved with dotenv, like check does in /deploy.
func lintWith(t testing.TB, compose, override []byte, v Variant, dotenv map[string]string) []Finding {
	t.Helper()
	return lintWithDB(t, compose, override, v, DBBundled, dotenv)
}

// lintWithDB is lintWith for a database mode.
func lintWithDB(t testing.TB, compose, override []byte, v Variant, db DBMode, dotenv map[string]string) []Finding {
	t.Helper()
	img := mustRef(t, variantImage)
	expected, err := renderEmbedded(bekup.Assets(), v, db, &img)
	if err != nil {
		t.Fatal(err)
	}
	if compose == nil {
		compose = expected
	}
	in := composeInput{root: "/deploy", compose: compose, override: override, dotenv: dotenv}
	return lintComposeFiles(&in, projectEnv(dotenv, nil, in.docs()...), v, variantImage, expectedDocs(expected, Overlays{}), false)
}

// evilService is a new service with the full hardening baseline; the
// bypass cases add one key to it.
const evilService = "  evil:\n    image: alpine:3@" + testDigest2 + "\n    user: \"1000:1000\"\n    read_only: true\n    cap_drop: [ALL]\n    security_opt: [\"no-new-privileges:true\"]\n"

// insertLineAfter inserts add after the first line equal to line.
func insertLineAfter(t testing.TB, s, line, add string) string {
	t.Helper()
	i := strings.Index(s, "\n"+line+"\n")
	if i < 0 {
		t.Fatalf("no line %q", line)
	}
	i += len(line) + 2
	return s[:i] + add + s[i:]
}

// TestLintBypasses: every reviewed bypass of the hardening lint is caught
// on the resolved model, both in a customized compose.yaml and in
// compose.override.yaml (D-205 amendment).
func TestLintBypasses(t *testing.T) {
	t.Parallel()
	img := mustRef(t, variantImage)
	src, err := RenderRelease(VariantTraefik, DBBundled, &img)
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	baseline := lintWith(t, nil, nil, VariantTraefik, nil)
	if len(baseline) > 0 {
		t.Fatalf("shipped file: %v", baseline)
	}
	for _, tc := range []struct {
		name string
		svc  string            // extra lines of the evil service (4 spaces)
		top  map[string]string // top-level section -> entries (2 spaces)
		env  map[string]string
		want string
	}{
		{name: "volumes_from", svc: "    volumes_from: [runner]\n", want: "`volumes_from`"},
		{name: "bind /var/run short", svc: "    volumes: [\"/var/run:/h\"]\n", want: "from outside the deployment directory"},
		{name: "bind /run short", svc: "    volumes: [\"/run:/h:ro\"]\n", want: "from outside the deployment directory"},
		{name: "bind / short", svc: "    volumes: [\"/:/host\"]\n", want: "from outside the deployment directory"},
		{name: "bind / long", svc: "    volumes:\n      - type: bind\n        source: /\n        target: /host\n", want: "from outside the deployment directory"},
		{name: "bind .. escape", svc: "    volumes: [\"./deploy/../..:/up\"]\n", want: "from outside the deployment directory"},
		{name: "bind home", svc: "    volumes: [\"~/.ssh:/k\"]\n", want: "from outside the deployment directory"},
		{name: "docker socket long", svc: "    volumes:\n      - type: bind\n        source: /var/run/docker.sock\n        target: /s\n", want: "container engine socket"},
		{name: "bind propagation", svc: "    volumes:\n      - type: bind\n        source: ./deploy\n        target: /d\n        bind:\n          propagation: rshared\n", want: "bind propagation"},
		{name: "image mount", svc: "    volumes:\n      - type: image\n        source: alpine:3\n        target: /i\n", want: "mount is not allowed"},
		{name: "secret file", top: map[string]string{"secrets": "  evil_s:\n    file: /etc/shadow\n"}, want: `secret "evil_s": file "/etc/shadow" is outside`},
		{name: "config file", top: map[string]string{"configs": "  evil_c:\n    file: ../../etc/passwd\n"}, want: `config "evil_c": file`},
		{name: "volume driver_opts bind", svc: "    volumes: [\"evilvol:/host\"]\n", top: map[string]string{"volumes": "  evilvol:\n    driver_opts: {type: none, o: bind, device: /}\n"}, want: `volume "evilvol": a volume driver or driver_opts`},
		{name: "external volume", top: map[string]string{"volumes": "  extvol:\n    external: true\n"}, want: "external volumes are not allowed"},
		{name: "host network by name", top: map[string]string{"networks": "  hostnet:\n    external: true\n    name: host\n"}, want: `network "hostnet"`},
		{name: "macvlan", top: map[string]string{"networks": "  lan:\n    driver: macvlan\n"}, want: "only bridge networks"},
		{name: "devices", svc: "    devices: [\"/dev/sda:/dev/sda\"]\n", want: "`devices`"},
		{name: "device_cgroup_rules", svc: "    device_cgroup_rules: [\"a *:* rwm\"]\n", want: "`device_cgroup_rules`"},
		{name: "pid service", svc: "    pid: service:postgres\n", want: "`pid: service:postgres`"},
		{name: "pid host", svc: "    pid: host\n", want: "`pid: host`"},
		{name: "ipc container", svc: "    ipc: container:abc\n", want: "`ipc: container:abc`"},
		{name: "ipc host", svc: "    ipc: host\n", want: "`ipc: host`"},
		{name: "network_mode service", svc: "    network_mode: service:postgres\n", want: "`network_mode: service:postgres`"},
		{name: "network_mode container", svc: "    network_mode: container:abc\n", want: "`network_mode: container:abc`"},
		{name: "network_mode host", svc: "    network_mode: host\n", want: "`network_mode: host`"},
		{name: "uts host", svc: "    uts: host\n", want: "`uts: host`"},
		{name: "cgroup host", svc: "    cgroup: host\n", want: "`cgroup: host`"},
		{name: "userns other", svc: "    userns_mode: keep-id\n", want: "`userns_mode: keep-id`"},
		{name: "post_start privileged", svc: "    post_start:\n      - command: [sh, -c, id]\n        privileged: true\n", want: "privileged post_start/pre_stop hook"},
		{name: "pre_stop root", svc: "    pre_stop:\n      - command: [sh, -c, id]\n        user: root\n", want: "hook runs as user \"root\""},
		{name: "post_start uid 0", svc: "    post_start:\n      - command: [sh]\n        user: \"0\"\n", want: "hook runs as user \"0\""},
		{name: "pre_start container", svc: "    pre_start:\n      - image: alpine:3@" + testDigest2 + "\n        privileged: true\n", want: "`pre_start`"},
		{name: "cap MAC_ADMIN", svc: "    cap_add: [MAC_ADMIN]\n", want: "capability MAC_ADMIN"},
		{name: "cap DAC_OVERRIDE", svc: "    cap_add: [DAC_OVERRIDE]\n", want: "capability DAC_OVERRIDE"},
		{name: "cap SETUID", svc: "    cap_add: [CAP_SETUID]\n", want: "capability SETUID"},
		{name: "cap SETGID", svc: "    cap_add: [setgid]\n", want: "capability SETGID"},
		{name: "cap MKNOD", svc: "    cap_add: [MKNOD]\n", want: "capability MKNOD"},
		{name: "cap SYS_TIME", svc: "    cap_add: [SYS_TIME]\n", want: "capability SYS_TIME"},
		{name: "cap NET_RAW", svc: "    cap_add: [NET_RAW]\n", want: "capability NET_RAW"},
		{name: "cap CHOWN elsewhere", svc: "    cap_add: [CHOWN]\n", want: "capability CHOWN"},
		{name: "seccomp profile", svc: "    security_opt: [\"no-new-privileges:true\", \"seccomp=/tmp/p.json\"]\n", want: `security_opt "seccomp=/tmp/p.json" is not allowed`},
		{name: "apparmor unconfined", svc: "    security_opt: [\"no-new-privileges:true\", \"apparmor:unconfined\"]\n", want: `security_opt "apparmor:unconfined"`},
		{name: "runtime", svc: "    runtime: runsc\n", want: "`runtime`"},
		{name: "cgroup_parent", svc: "    cgroup_parent: /\n", want: "`cgroup_parent`"},
		{name: "sysctls", svc: "    sysctls: {net.ipv4.ip_forward: 1}\n", want: "`sysctls`"},
		{name: "use_api_socket", svc: "    use_api_socket: true\n", want: "`use_api_socket`"},
		{name: "provider", svc: "    provider:\n      type: evil\n", want: "`provider`"},
		{name: "privileged", svc: "    privileged: true\n", want: "`privileged`"},
		{name: "env_file outside", svc: "    env_file: [/etc/environment]\n", want: "env_file"},
		{name: "root user", svc: "    user: \"0\"\n", want: `user "0" is not a numeric non-root`},
		{name: "named user", svc: "    user: root\n", want: `user "root"`},
		{name: "root group", svc: "    user: \"1000:0\"\n", want: `user "1000:0"`},
		{name: "build", svc: "    build: .\n", want: "`build`"},
		{name: "unpinned", svc: "    image: alpine:3\n", want: `image "alpine:3" is not digest-pinned`},
		// ${VAR} from .env: the resolved value is linted.
		{name: "env network_mode", svc: "    network_mode: ${EVIL_NET:-none}\n", env: map[string]string{"EVIL_NET": "host"}, want: "`network_mode: host`"},
		{name: "env cap_add", svc: "    cap_add: [\"${EVIL_CAP:-NET_BIND_SERVICE}\"]\n", env: map[string]string{"EVIL_CAP": "SYS_ADMIN"}, want: "capability SYS_ADMIN"},
		{name: "env security_opt", svc: "    security_opt: [\"no-new-privileges:true\", \"${EVIL_SO:-no-new-privileges}\"]\n", env: map[string]string{"EVIL_SO": "seccomp=unconfined"}, want: `"seccomp=unconfined"`},
		{name: "env volume", svc: "    volumes: [\"${EVIL_SRC:-./deploy}:/h\"]\n", env: map[string]string{"EVIL_SRC": "/"}, want: "from outside the deployment directory"},
		{name: "env image", svc: "    image: ${EVIL_IMG:-alpine:3@" + testDigest2 + "}\n", env: map[string]string{"EVIL_IMG": "alpine:latest"}, want: `"alpine:latest" is not digest-pinned`},
	} {
		// The evil service with a later key wins over the baseline key in
		// the override (merge) and is a duplicate-free mapping in
		// compose.yaml (the baseline keys the case sets are dropped).
		body := evilService
		for _, l := range strings.Split(tc.svc, "\n") {
			if k, _, ok := strings.Cut(strings.TrimSpace(l), ":"); ok && strings.HasPrefix(l, "    ") && !strings.HasPrefix(l, "     ") {
				var kept []string
				for _, b := range strings.Split(body, "\n") {
					if !strings.HasPrefix(b, "    "+k+":") {
						kept = append(kept, b)
					}
				}
				body = strings.Join(kept, "\n")
			}
		}
		body += tc.svc
		t.Run(tc.name+"/compose.yaml", func(t *testing.T) {
			t.Parallel()
			c := insertLineAfter(t, s, "services:", body)
			for sec, entries := range tc.top {
				c = insertLineAfter(t, c, sec+":", entries)
			}
			if fs := lintWith(t, []byte(c), nil, VariantTraefik, tc.env); !hasFinding(fs, SeverityError, tc.want) {
				t.Fatalf("no error %q in %v", tc.want, fs)
			}
		})
		t.Run(tc.name+"/override", func(t *testing.T) {
			t.Parallel()
			var o strings.Builder
			o.WriteString("services:\n" + body)
			for sec, entries := range tc.top {
				o.WriteString(sec + ":\n" + entries)
			}
			if fs := lintWith(t, nil, []byte(o.String()), VariantTraefik, tc.env); !hasFinding(fs, SeverityError, tc.want) {
				t.Fatalf("no error %q in %v", tc.want, fs)
			}
		})
	}
}

// TestLintResolvedModel: multi-document files, YAML merge-key precedence,
// !reset/!override and removed hardening are judged like docker compose
// resolves them.
func TestLintResolvedModel(t *testing.T) {
	t.Parallel()
	img := mustRef(t, variantImage)
	src, err := RenderRelease(VariantTraefik, DBBundled, &img)
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	fsys := bekup.Assets()
	const anchors = "x-evilcaps: &evilcaps\n  cap_add: [SYS_ADMIN]\nx-goodcaps: &goodcaps\n  cap_add: [CHOWN]\n"
	setupCaps := "    cap_add: [CHOWN]\n    security_opt:\n      - no-new-privileges:true\n    network_mode: none\n"
	if strings.Count(s, setupCaps) != 1 {
		t.Fatal("setup block not found once")
	}
	withAnchors := insertLineAfter(t, s, "name: bekup", anchors)
	for _, tc := range []struct{ name, compose, want string }{
		// H4: the earlier mapping of a merge list wins.
		{"merge list order", strings.Replace(withAnchors, setupCaps, "    <<: [*evilcaps, *goodcaps]\n"+setupCaps[len("    cap_add: [CHOWN]\n"):], 1), "capability SYS_ADMIN"},
		// H4: an explicit key wins over a later merge key.
		{"explicit beats merge", strings.Replace(withAnchors, setupCaps, "    cap_add: [SYS_ADMIN]\n    <<: *goodcaps\n"+setupCaps[len("    cap_add: [CHOWN]\n"):], 1), "capability SYS_ADMIN"},
		{"runner host bind", strings.Replace(s, "      - /var/run/docker.sock:/var/run/docker.sock:ro\n", "      - /var/run/docker.sock:/var/run/docker.sock:ro\n      - /:/host:ro\n", 1), `service "runner" bind-mounts "/"`},
		{"runner socket rw", strings.Replace(s, "      - /var/run/docker.sock:/var/run/docker.sock:ro\n", "      - /var/run/docker.sock:/var/run/docker.sock\n", 1), "mount the Docker socket read-only"},
		{"runner socket elsewhere", strings.Replace(s, "      - /var/run/docker.sock:/var/run/docker.sock:ro\n", "      - /var/run/docker.sock:/tmp/d.sock:ro\n", 1), "container engine socket"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.compose == s {
				t.Fatal("edit did not apply")
			}
			fs := lintWith(t, []byte(tc.compose), nil, VariantTraefik, nil)
			if !hasFinding(fs, SeverityError, tc.want) {
				t.Fatalf("no error %q in %v", tc.want, fs)
			}
			// Never recognized as a release file.
			if _, err := inspectComposeData(fsys, composeInput{compose: []byte(tc.compose)}, "", ""); err == nil || !strings.Contains(err.Error(), "customized") {
				t.Fatalf("recognized: %v", err)
			}
			info, err := inspectComposeData(fsys, composeInput{compose: []byte(tc.compose)}, VariantTraefik, "")
			if err != nil || info.Level != RecognizedCustom || !info.HasErrors() {
				t.Fatalf("customized: %+v %v", info, err)
			}
		})
	}
	if fs := lintWith(t, []byte(strings.Replace(withAnchors, setupCaps, "    cap_add: [SYS_ADMIN]\n    <<: *goodcaps\n"+setupCaps[len("    cap_add: [CHOWN]\n"):], 1)), nil, VariantTraefik, nil); !hasFinding(fs, SeverityError, "the setup service differs") {
		t.Fatalf("setup merge trick: %v", fs)
	}
	for _, tc := range []struct{ name, override, want string }{
		{"multi-document", "services: {}\n---\nservices:\n  serve:\n    privileged: true\n", "more than one YAML document"},
		{"merge list order", anchors + "services:\n  serve:\n    <<: [*evilcaps, *goodcaps]\n", "capability SYS_ADMIN"},
		{"explicit beats merge", anchors + "services:\n  serve:\n    cap_add: [SYS_ADMIN]\n    <<: *goodcaps\n", "capability SYS_ADMIN"},
		{"setup via merge", anchors + "services:\n  setup:\n    <<: *evilcaps\n", "the override changes the setup service"},
		{"setup reset", "services:\n  setup:\n    network_mode: !reset null\n", "the override changes the setup service"},
		{"reset cap_drop", "services:\n  serve:\n    cap_drop: !reset []\n", "`cap_drop` must contain ALL"},
		{"override cap_drop", "services:\n  serve:\n    cap_drop: !override [NET_RAW]\n", "`cap_drop` must contain ALL"},
		{"drop no-new-privileges", "services:\n  serve:\n    security_opt: !reset []\n", "must contain no-new-privileges:true"},
		{"no-new-privileges false", "services:\n  serve:\n    security_opt: !override [\"no-new-privileges:false\"]\n", `security_opt "no-new-privileges:false" is not allowed`},
		{"read_only false", "services:\n  postgres:\n    read_only: false\n", "must be read-only"},
		{"user 0", "services:\n  serve:\n    user: \"0\"\n", `user "0"`},
		{"user reset", "services:\n  traefik:\n    user: !reset null\n", `service "traefik": user ""`},
		{"runner host bind", "services:\n  runner:\n    volumes: [\"/:/host\"]\n", `service "runner" bind-mounts "/"`},
		{"runner etc", "services:\n  runner:\n    volumes: [\"/etc:/etc2:ro\"]\n", `service "runner" bind-mounts "/etc"`},
		{"root init bind", "services:\n  traefik-init:\n    volumes: [\"./deploy:/d\"]\n", "runs as root and may only mount named volumes"},
		{"root init network", "services:\n  traefik-init:\n    network_mode: !reset null\n", "must keep `network_mode: none`"},
		{"include", "include: [x.yaml]\n", "`include` and `extends.file`"},
	} {
		t.Run("override/"+tc.name, func(t *testing.T) {
			t.Parallel()
			if fs := lintWith(t, nil, []byte(tc.override), VariantTraefik, nil); !hasFinding(fs, SeverityError, tc.want) {
				t.Fatalf("no error %q in %v", tc.want, fs)
			}
		})
	}
	// A multi-document compose.yaml is refused before anything else.
	if _, err := inspectComposeData(fsys, composeInput{compose: []byte(s + "---\nservices:\n  serve:\n    privileged: true\n")}, VariantTraefik, ""); err == nil || !strings.Contains(err.Error(), "more than one YAML document") {
		t.Fatalf("multi-document compose.yaml: %v", err)
	}
	// ... and so is a multi-document override at init/upgrade.
	if _, err := inspectComposeData(fsys, composeInput{compose: src, override: []byte("{}\n---\n{}\n")}, "", ""); err == nil || !strings.Contains(err.Error(), "more than one YAML document") {
		t.Fatalf("multi-document override: %v", err)
	}
	// Allowed: the documented runner overrides.
	for _, ok := range []string{
		"services:\n  runner:\n    volumes: !override [\"/run/user/1000/docker.sock:/var/run/docker.sock:ro\"]\n",
		"services:\n  serve:\n    volumes: [\"./backups:/var/lib/bekup/backups/disk\"]\n",
		// docs/operations.md: Prometheus on the metrics network.
		"services:\n  prometheus:\n    image: prom/prometheus:v3@" + testDigest2 + "\n    user: \"65534:65534\"\n    read_only: true\n    cap_drop: [ALL]\n    security_opt: [\"no-new-privileges:true\"]\n    volumes:\n      - ./prometheus.yml:/etc/prometheus/prometheus.yml:ro\n      - prometheus-data:/prometheus\n    networks: [metrics]\nvolumes:\n  prometheus-data: {}\n",
		// docs/installation/reverse-proxy.md: static TLS.
		"services:\n  traefik:\n    environment:\n      PROXY_TLS_MODE: static\n    volumes:\n      - type: bind\n        source: ./proxy-tls\n        target: /certs\n        read_only: true\n",
		"",
	} {
		if fs := lintWith(t, nil, []byte(ok), VariantTraefik, nil); hasErrors(fs) {
			t.Errorf("override %q: %v", ok, fs)
		}
	}
	if fs := lintWith(t, nil, []byte("services:\n  runner:\n    userns_mode: host\n"), VariantTraefik, nil); hasErrors(fs) || !hasFinding(fs, SeverityWarning, "userns_mode: host") {
		t.Errorf("userns_mode host: %v", fs)
	}
}

// TestComposeEnvInterpolation: .env values change the resolved model the
// same way for recognition and lint, and the environment overrides .env
// for referenced variables.
func TestComposeEnvInterpolation(t *testing.T) {
	t.Parallel()
	img := mustRef(t, variantImage)
	src, err := RenderRelease(VariantOwnProxy, DBBundled, &img)
	if err != nil {
		t.Fatal(err)
	}
	fsys := bekup.Assets()
	env := map[string]string{"SERVE_METRICS_IP": "10.213.252.11"}
	info, err := inspectComposeData(fsys, composeInput{compose: src, dotenv: env}, "", "")
	if err != nil || info.Level != RecognizedExact || len(info.Findings) > 0 {
		t.Fatalf("%+v %v", info, err)
	}
	// A literal instead of the variable is the same model only while .env
	// agrees; with another value it is customized.
	lit := strings.Replace(string(src), "${SERVE_METRICS_IP:-10.213.252.10}", "10.213.252.10", 1)
	if lit == string(src) {
		t.Fatal("edit did not apply")
	}
	if _, err := inspectComposeData(fsys, composeInput{compose: []byte(lit), dotenv: env}, "", ""); err == nil || !strings.Contains(err.Error(), "customized") {
		t.Fatalf("literal with a different .env: %v", err)
	}
	got := projectEnv(map[string]string{"A": "1", "B": "2"}, func(k string) (string, bool) {
		if k == "A" || k == "C" {
			return "env", true
		}
		return "", false
	}, composeSrc{name: ComposeFile, data: []byte("x: ${A} $B ${C:-d}\n")})
	if got["A"] != "env" || got["B"] != "2" || got["C"] != "env" {
		t.Fatalf("projectEnv %v", got)
	}
}

// reviewedServiceKeys lists every service attribute of the pinned
// compose-go version with how the lint treats it on services the operator
// adds (shipped services are pinned attribute by attribute to the shipped
// definition, composepin.go, except operatorTunable). A compose-go upgrade
// that adds an attribute fails this test until the new key is reviewed
// (and denied in lintService when it can reach the host).
var reviewedServiceKeys = map[string]string{
	// Denied or restricted in lintService.
	"build": "deny", "dockerfile": "deny", "privileged": "deny", "devices": "deny",
	"device_cgroup_rules": "deny", "volumes_from": "deny", "gpus": "deny", "pre_start": "deny",
	"provider": "deny", "develop": "deny", "use_api_socket": "deny", "runtime": "deny",
	"cgroup_parent": "deny", "isolation": "deny", "credential_spec": "deny", "storage_opt": "deny",
	"volume_driver": "deny", "models": "deny", "sysctls": "deny",
	"image": "pinned", "network_mode": "none only", "net": "none only", "pid": "empty",
	"ipc": "empty/none/private", "uts": "empty", "cgroup": "empty/private", "userns_mode": "host warns",
	"read_only": "required", "cap_drop": "ALL required", "cap_add": "allowlist",
	"security_opt": "no-new-privileges only", "user": "numeric non-root",
	"post_start": "no privileged/root", "pre_stop": "no privileged/root",
	"volumes": "inside the directory", "env_file": "inside the directory",
	"label_file": "inside the directory", "ports": "proxy only", "deploy": "no devices",
	// Allowed: no host access beyond what the checked keys grant.
	"name": "ok", "profiles": "ok", "restart": "ok", "scale": "ok", "attach": "ok",
	"container_name": "not on bekup's networks", "extends": "same file only (loader)", "external_links": "deny",
	"links": "ok", "annotations": "ok", "blkio_config": "ok", "cpu_count": "ok",
	"cpu_percent": "ok", "cpu_period": "ok", "cpu_quota": "ok", "cpu_rt_period": "deny",
	"cpu_rt_runtime": "deny", "cpus": "ok", "cpuset": "ok", "cpu_shares": "ok", "command": "ok",
	"configs": "ok", "dns": "ok", "dns_opt": "ok", "dns_search": "ok", "domainname": "ok",
	"entrypoint": "ok", "environment": "ok", "extra_hosts": "ok", "group_add": "deny",
	"hostname": "ok", "init": "ok", "labels": "no traefik.*/com.docker.*", "logging": "json-file/local/none", "log_driver": "json-file/local/none",
	"log_opt": "size/rotation only", "mem_limit": "ok", "mem_reservation": "ok", "memswap_limit": "ok",
	"mem_swappiness": "ok", "mac_address": "ok", "networks": "own, metrics (frontend: full)", "oom_kill_disable": "deny",
	"oom_score_adj": ">= 0", "pids_limit": ">= 0", "platform": "ok", "pull_policy": "ok",
	"pull_refresh_after": "ok", "secrets": "ok", "shm_size": "<= 1 GiB", "stop_grace_period": "ok",
	"stop_signal": "ok", "tmpfs": "ok", "ulimits": "nofile/nproc, bounded", "umask": "ok", "working_dir": "ok",
	"depends_on": "ok", "expose": "ok", "healthcheck": "ok", "stdin_open": "ok", "tty": "ok",
}

func TestServiceKeysReviewed(t *testing.T) {
	t.Parallel()
	var walk func(rt reflect.Type)
	walk = func(rt reflect.Type) {
		for f := range rt.Fields() {
			tag, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
			switch {
			case f.Anonymous:
				walk(f.Type)
			case tag == "" || tag == "-" || strings.HasPrefix(tag, "#"):
			default:
				if _, ok := reviewedServiceKeys[tag]; !ok {
					t.Errorf("service attribute %q (compose-go %s) is not reviewed for the hardening lint", tag, f.Name)
				}
			}
		}
	}
	walk(reflect.TypeOf(types.ServiceConfig{}))
	// Top-level attributes: services, networks, volumes, secrets and
	// configs are linted, jobs and models denied.
	top := []string{"name", "services", "jobs", "networks", "volumes", "secrets", "configs", "models"}
	for f := range reflect.TypeOf(types.Project{}).Fields() {
		tag, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if tag != "" && tag != "-" && !strings.HasPrefix(tag, "#") && !slices.Contains(top, tag) {
			t.Errorf("top-level attribute %q is not reviewed for the hardening lint", tag)
		}
	}
}
