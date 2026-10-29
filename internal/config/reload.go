package config

import (
	"math"
	"reflect"
	"slices"
)

// Runtime config reload support (D-200). A reload re-reads bekup.toml,
// applies only the keys tagged reload:"live" and reports every other
// changed key as needing a restart. These helpers work on key paths only;
// callers must never show or store the values.

// LiveKeys returns the paths ("section.key") of every bekup.toml key a
// runtime reload applies, in declaration order.
func LiveKeys() []string {
	var c Bekup
	var out []string
	fields := fieldsOf(&c)
	for i := range fields {
		if fields[i].Live {
			out = append(out, fields[i].Path)
		}
	}
	return out
}

// IsLiveKey reports whether path is a live key.
func IsLiveKey(path string) bool { return slices.Contains(LiveKeys(), path) }

// KeyPaths returns the path of every bekup.toml key in declaration order.
func KeyPaths() []string {
	var c Bekup
	fields := fieldsOf(&c)
	out := make([]string, len(fields))
	for i := range fields {
		out[i] = fields[i].Path
	}
	return out
}

// Diff returns the paths of the keys whose values differ between oldCfg
// and newCfg, in declaration order. A nil and an empty list are equal.
func Diff(oldCfg, newCfg *Bekup) []string {
	a, b := fieldsOf(oldCfg), fieldsOf(newCfg)
	var out []string
	for i := range a {
		if !sameValue(a[i].value, b[i].value) {
			out = append(out, a[i].Path)
		}
	}
	return out
}

func sameValue(a, b reflect.Value) bool {
	if a.Kind() == reflect.Slice {
		if a.Len() != b.Len() {
			return false
		}
		for i := range a.Len() {
			if !sameValue(a.Index(i), b.Index(i)) {
				return false
			}
		}
		return true
	}
	if a.Kind() == reflect.Float64 {
		// NaN never equals itself; treat two NaNs as unchanged (validation
		// refuses them anyway).
		x, y := a.Float(), b.Float()
		return x == y || (math.IsNaN(x) && math.IsNaN(y))
	}
	return a.Interface() == b.Interface()
}

// WithLive returns a deep copy of running in which only the live keys
// carry the values of next. Restart-only keys keep their running values,
// so the result is what the process would run with after a reload.
func WithLive(running, next *Bekup) *Bekup {
	out := cloneBekup(running)
	dst, src := fieldsOf(out), fieldsOf(next)
	for i := range dst {
		if !dst[i].Live {
			continue
		}
		dst[i].value.Set(cloneValue(src[i].value))
	}
	return out
}

// Clone returns a deep copy of c (lists are copied).
func (c *Bekup) Clone() *Bekup { return cloneBekup(c) }

func cloneBekup(c *Bekup) *Bekup {
	out := *c
	fields := fieldsOf(&out)
	for i := range fields {
		if fields[i].value.Kind() == reflect.Slice {
			fields[i].value.Set(cloneValue(fields[i].value))
		}
	}
	return &out
}

func cloneValue(v reflect.Value) reflect.Value {
	if v.Kind() != reflect.Slice || v.IsNil() {
		return v
	}
	c := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
	reflect.Copy(c, v)
	return c
}

// ValidateBekup validates c as a loaded bekup.toml would be under dev mode
// dev (the reload re-validates the merged config under the startup dev
// mode, D-200). Every error is a *KeyError.
func ValidateBekup(c *Bekup, dev bool) []error { return validateBekup(c, dev) }
