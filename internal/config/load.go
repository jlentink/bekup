package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Source tells where the effective value of a key came from.
type Source string

// Sources in increasing order of precedence.
const (
	SourceDefault Source = "default"
	SourceFile    Source = "file"
	SourceEnv     Source = "env"
	SourceFlag    Source = "flag"
)

// File names and well-known environment variables.
const (
	BekupFileName  = "bekup.toml"
	RunnerFileName = "runner.toml"

	// EnvConfig names an explicit config file, like --config.
	EnvConfig = "BEKUP_CONFIG"
	// EnvDev enables dev mode, like --dev.
	EnvDev = "BEKUP_DEV"
)

// ErrNoConfig is returned when no config file is found outside dev mode.
var ErrNoConfig = errors.New("no config file found")

// DefaultSearchPaths returns the implicit search path for a config file name
// (after --config / BEKUP_CONFIG): the working directory, then /etc/bekup.
func DefaultSearchPaths(name string) []string {
	return []string{"./" + name, "/etc/bekup/" + name}
}

// Options control how a config file is located and overridden.
type Options struct {
	// ConfigPath is the --config argument. When empty, BEKUP_CONFIG is used.
	// An explicit path that does not exist is a hard error (no fallback).
	ConfigPath string
	// Dev is the --dev flag.
	Dev bool
	// Flags are CLI overrides keyed by "section.key"; they have the highest
	// precedence.
	Flags map[string]string
	// LookupEnv reads environment variables; nil means os.LookupEnv.
	LookupEnv func(string) (string, bool)
	// Environ lists the environment as KEY=VALUE pairs, used only to warn
	// about unknown BEKUP_* variables; nil means os.Environ.
	Environ func() []string
	// SearchPaths replaces DefaultSearchPaths (used by tests).
	SearchPaths []string
}

// Result is a loaded, validated configuration.
type Result[T any] struct {
	Config *T
	// Path is the file that was loaded; empty when running on defaults in
	// dev mode.
	Path string
	// Dev reports whether dev mode is active.
	Dev bool
	// Sources maps every "section.key" to the source of its effective value.
	Sources map[string]Source
	// Warnings must be logged at startup (dev mode, inline secrets, ...).
	Warnings []string
}

// Fields returns every key of the loaded config with its effective value.
func (r *Result[T]) Fields() []Field { return fieldsOf(r.Config) }

// LoadBekup locates, parses, overrides and validates bekup.toml.
func LoadBekup(opts Options) (*Result[Bekup], error) {
	cfg := DefaultBekup()
	return load(&cfg, BekupFileName, opts, func(c *Bekup, dev bool) {
		if dev && c.Server.BaseURL == "" {
			c.Server.BaseURL = DevBaseURL
		}
	}, validateBekup, (*Bekup).warnings)
}

// LoadRunner locates, parses, overrides and validates runner.toml.
func LoadRunner(opts Options) (*Result[Runner], error) {
	cfg := DefaultRunner()
	return load(&cfg, RunnerFileName, opts, nil, validateRunner, (*Runner).warnings)
}

const devEnabledPath = "dev.enabled"

func load[T any](cfg *T, name string, opts Options, devDefaults func(*T, bool),
	validate func(*T, bool) []error, warnings func(*T, bool) []string,
) (*Result[T], error) {
	lookup := opts.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	fields := fieldsOf(cfg)
	byPath := make(map[string]Field, len(fields))
	sources := make(map[string]Source, len(fields))
	for i := range fields {
		f := &fields[i]
		byPath[f.Path] = *f
		sources[f.Path] = SourceDefault
	}

	// Dev mode must be known before the search so that a missing file is
	// allowed. A [dev] enabled = true in the file also enables it afterwards.
	dev, devSource, err := preDev(opts, lookup, byPath)
	if err != nil {
		return nil, err
	}

	path, err := findConfig(name, opts, lookup, dev)
	if err != nil {
		return nil, &FileError{Err: err}
	}

	if path != "" {
		data, err := readSafeFile(path, false, maxConfigBytes)
		if err != nil {
			return nil, &FileError{Err: fmt.Errorf("config file: %w", err)}
		}
		present, err := decodeStrict(path, data, cfg)
		if err != nil {
			return nil, err
		}
		for p := range present {
			if _, ok := sources[p]; ok {
				sources[p] = SourceFile
			}
		}
	}

	if errs := applyOverrides(fields, byPath, sources, lookup, opts.Flags); len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	if f, ok := byPath[devEnabledPath]; ok {
		if dev {
			f.value.SetBool(true)
			if devSource != "" {
				sources[devEnabledPath] = devSource
			}
		}
		dev = f.value.Bool()
	}
	if devDefaults != nil {
		devDefaults(cfg, dev)
	}

	var errs []error
	res := &Result[T]{Config: cfg, Path: path, Dev: dev, Sources: sources}
	if dev {
		res.Warnings = append(res.Warnings,
			"DEV MODE IS ENABLED: security guardrails are relaxed (http base_url, no trusted_proxies, inline secrets). Never use dev mode in production.")
	}
	for i := range fields {
		f := &fields[i]
		if !f.InlineSecret || f.IsZero() {
			continue
		}
		if !dev {
			errs = append(errs, &KeyError{Path: f.Path, Msg: fmt.Sprintf("%s: inline secrets are rejected outside dev mode (set via %s); use %s_file", f.Path, sources[f.Path], f.Key)})
			continue
		}
		res.Warnings = append(res.Warnings, fmt.Sprintf("dev mode: using inline secret %s; use %s_file outside dev mode", f.Path, f.Key))
	}
	errs = append(errs, validate(cfg, dev)...)
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	res.Warnings = append(res.Warnings, warnings(cfg, dev)...)
	environ := opts.Environ
	if environ == nil {
		environ = os.Environ
	}
	res.Warnings = append(res.Warnings, envWarnings(environ(), fields)...)
	return res, nil
}

// applyOverrides applies BEKUP_<SECTION>_<KEY> env vars and then CLI flag
// overrides, recording the source of every changed key.
func applyOverrides(fields []Field, byPath map[string]Field, sources map[string]Source,
	lookup func(string) (string, bool), flags map[string]string,
) []error {
	var errs []error
	for i := range fields {
		f := &fields[i]
		v, ok := lookup(f.Env)
		if !ok {
			continue
		}
		if err := f.setFromString(v); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f.Env, err))
			continue
		}
		sources[f.Path] = SourceEnv
	}
	flagKeys := make([]string, 0, len(flags))
	for k := range flags {
		flagKeys = append(flagKeys, k)
	}
	slices.Sort(flagKeys)
	for _, k := range flagKeys {
		f, ok := byPath[k]
		if !ok {
			errs = append(errs, fmt.Errorf("flag override: unknown key %q", k))
			continue
		}
		if f.InlineSecret {
			errs = append(errs, fmt.Errorf("%s: secrets cannot be passed as command-line flags", k))
			continue
		}
		if err := f.setFromString(flags[k]); err != nil {
			errs = append(errs, fmt.Errorf("flag override: %w", err))
			continue
		}
		sources[k] = SourceFlag
	}
	return errs
}

// preDev determines dev mode from --dev, BEKUP_DEV, and the dev.enabled
// env/flag overrides.
func preDev(opts Options, lookup func(string) (string, bool), byPath map[string]Field) (bool, Source, error) {
	if opts.Dev {
		return true, SourceFlag, nil
	}
	if v, ok := opts.Flags[devEnabledPath]; ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return false, "", fmt.Errorf("flag override %s: invalid bool %q", devEnabledPath, v)
		}
		if b {
			return true, SourceFlag, nil
		}
	}
	names := []string{EnvDev}
	if f, ok := byPath[devEnabledPath]; ok {
		names = append(names, f.Env)
	}
	for _, name := range names {
		v, ok := lookup(name)
		if !ok || v == "" {
			continue
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			return false, "", fmt.Errorf("%s: invalid bool %q", name, v)
		}
		if b {
			return true, SourceEnv, nil
		}
	}
	return false, "", nil
}

// findConfig applies the search path: --config / BEKUP_CONFIG (missing is a
// hard error), then the search paths; the first existing candidate wins.
func findConfig(name string, opts Options, lookup func(string) (string, bool), dev bool) (string, error) {
	explicit, from := opts.ConfigPath, "--config"
	if explicit == "" {
		if v, ok := lookup(EnvConfig); ok && v != "" {
			explicit, from = v, EnvConfig
		}
	}
	if explicit != "" {
		if _, err := os.Lstat(explicit); err != nil {
			return "", fmt.Errorf("config file %s (from %s): %w (an explicit config path never falls back to the search path)", explicit, from, err)
		}
		return explicit, nil
	}
	paths := opts.SearchPaths
	if paths == nil {
		paths = DefaultSearchPaths(name)
	}
	for _, p := range paths {
		_, err := os.Lstat(p)
		if err == nil {
			return p, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("config file %s: %w", p, err)
		}
	}
	if dev {
		return "", nil
	}
	return "", fmt.Errorf("%w: searched %s; pass --config <path> or set %s (or use --dev for development)",
		ErrNoConfig, strings.Join(paths, ", "), EnvConfig)
}

// decodeStrict decodes data into cfg, rejecting unknown keys, and returns the
// set of "section.key" paths present in the document.
func decodeStrict(path string, data []byte, cfg any) (map[string]struct{}, error) {
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(cfg); err != nil {
		return nil, formatDecodeError(path, err)
	}
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil, formatDecodeError(path, err)
	}
	present := make(map[string]struct{})
	for section, v := range doc {
		table, ok := v.(map[string]any)
		if !ok {
			continue
		}
		for key := range table {
			present[section+"."+key] = struct{}{}
		}
	}
	return present, nil
}

func formatDecodeError(path string, err error) error {
	var sme *toml.StrictMissingError
	if errors.As(err, &sme) {
		errs := make([]error, 0, len(sme.Errors))
		for i := range sme.Errors {
			e := &sme.Errors[i]
			row, col := e.Position()
			key := strings.Join(e.Key(), ".")
			errs = append(errs, &KeyError{Path: key, Msg: fmt.Sprintf("%s:%d:%d: unknown key %q", path, row, col, key)})
		}
		return errors.Join(errs...)
	}
	var de *toml.DecodeError
	if errors.As(err, &de) {
		row, col := de.Position()
		msg := strings.TrimPrefix(de.Error(), "toml: ")
		if key := de.Key(); len(key) > 0 {
			k := strings.Join(key, ".")
			return &KeyError{Path: k, Msg: fmt.Sprintf("%s:%d:%d: key %q: %s", path, row, col, k, msg)}
		}
		return &SyntaxError{Err: fmt.Errorf("%s:%d:%d: %s", path, row, col, msg)}
	}
	return &SyntaxError{Err: fmt.Errorf("%s: %w", path, err)}
}
