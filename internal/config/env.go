package config

import (
	"fmt"
	"slices"
	"strings"
)

// NonKeyEnvVars are documented BEKUP_* environment variables that are not
// configuration keys: the loader's own switches plus variables read by the
// compose files, dev tooling and tests. They never trigger the unknown
// variable warning.
var NonKeyEnvVars = []string{
	EnvConfig,             // explicit config file (like --config)
	EnvDev,                // dev mode alias (like --dev)
	"BEKUP_IMAGE",         // compose: image reference
	"BEKUP_DOMAIN",        // compose/proxy profiles: public host name
	"BEKUP_DEV_BASE_URL",  // dev compose: base URL passed to serve
	"BEKUP_DEV_TLS_DIR",   // dev compose: local TLS certificates
	"BEKUP_DEV_BACKEND",   // web dev server: backend URL
	"BEKUP_WEB_BUILD",     // dev compose: web build switch
	"BEKUP_WEB_OUT_DIR",   // web build: output directory
	"BEKUP_UPDATE_SCHEMA", // tests: regenerate internal/db/schema.sql
}

// LegacyEnvNames maps shorthand variable names used in older plan and
// decision text to the canonical BEKUP_<SECTION>_<KEY> names (D-097). The
// shorthand names are not read; they only produce a "did you mean" warning.
var LegacyEnvNames = map[string]string{
	"BEKUP_BASE_URL":         "BEKUP_SERVER_BASE_URL",
	"BEKUP_TRUSTED_PROXIES":  "BEKUP_SERVER_TRUSTED_PROXIES",
	"BEKUP_MASTER_KEY_FILE":  "BEKUP_SECURITY_MASTER_KEY_FILE",
	"BEKUP_DISK_ROOT":        "BEKUP_STORAGE_DISK_ROOT",
	"BEKUP_SCRATCH_DIR":      "BEKUP_STORAGE_SCRATCH_DIR",
	"BEKUP_RUNNER_CONTAINER": "BEKUP_DOCKER_SELF_CONTAINER",
}

// envWarnings returns a warning for every BEKUP_* variable in environ that
// is neither a key of the loaded file (own) nor a documented non-key
// variable. Only variable names are reported, never values.
func envWarnings(environ []string, own []Field) []string {
	ownEnv := make(map[string]bool, len(own))
	for i := range own {
		ownEnv[own[i].Env] = true
	}
	b, r := DefaultBekup(), DefaultRunner()
	otherEnv := make(map[string]bool)
	all := append(fieldsOf(&b), fieldsOf(&r)...)
	for i := range all {
		otherEnv[all[i].Env] = true
	}
	var names []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, EnvPrefix) && !ownEnv[name] && !slices.Contains(NonKeyEnvVars, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	names = slices.Compact(names)
	out := make([]string, 0, len(names))
	for _, name := range names {
		switch {
		case LegacyEnvNames[name] != "":
			out = append(out, fmt.Sprintf("environment variable %s is not a configuration key and is ignored; did you mean %s?", name, LegacyEnvNames[name]))
		case otherEnv[name]:
			out = append(out, fmt.Sprintf("environment variable %s configures the other config file (bekup.toml vs runner.toml) and is ignored by this command", name))
		default:
			out = append(out, fmt.Sprintf("environment variable %s does not match any configuration key and is ignored (check for typos; see docs/configuration.md)", name))
		}
	}
	return out
}
