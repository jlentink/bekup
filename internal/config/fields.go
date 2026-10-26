package config

import (
	"encoding"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// EnvPrefix is the prefix of every environment override.
const EnvPrefix = "BEKUP_"

// Struct tags used on the config structs:
//
//	toml:"name"        TOML key (section or key name)
//	doc:"..."          one-line description, used for docs and the reference
//	secret:"inline"    an inline secret value; only accepted in dev mode
//	secret:"file"      a path to a secret file (read with ReadSecretFile)
const (
	secretInline = "inline"
	secretFile   = "file"
)

// Field describes one leaf key of a config file. Config files have exactly
// two levels: a section table and scalar (or string-list) keys.
type Field struct {
	value   reflect.Value
	Section string // TOML section name, e.g. "server"
	Key     string // TOML key name, e.g. "base_url"
	Path    string // "section.key"
	Env     string // BEKUP_SECTION_KEY
	Doc     string // description from the doc tag
	Type    string // human-readable type: string, bool, int, float, duration, list of strings
	// InlineSecret marks keys that hold a secret value directly. They are
	// rejected outside dev mode.
	InlineSecret bool
	// SecretFile marks keys that hold the path of a secret file.
	SecretFile bool
}

// Value returns the current value of the field.
func (f Field) Value() any { return f.value.Interface() }

// IsZero reports whether the field holds its type's zero value.
func (f Field) IsZero() bool { return f.value.IsZero() }

// fieldsOf walks a pointer to a config struct (Bekup or Runner) and returns
// every leaf key in declaration order.
func fieldsOf(cfg any) []Field {
	root := reflect.ValueOf(cfg)
	if root.Kind() != reflect.Pointer || root.Elem().Kind() != reflect.Struct {
		panic(fmt.Sprintf("config: fieldsOf needs a pointer to a struct, got %T", cfg))
	}
	root = root.Elem()
	rt := root.Type()
	var out []Field
	for i := range rt.NumField() {
		sf := rt.Field(i)
		section := tomlName(sf)
		if section == "" {
			continue
		}
		sv := root.Field(i)
		if sv.Kind() != reflect.Struct {
			panic(fmt.Sprintf("config: top-level field %s must be a section struct", sf.Name))
		}
		st := sv.Type()
		for j := range st.NumField() {
			kf := st.Field(j)
			key := tomlName(kf)
			if key == "" {
				continue
			}
			v := sv.Field(j)
			out = append(out, Field{
				value:        v,
				Section:      section,
				Key:          key,
				Path:         section + "." + key,
				Env:          EnvPrefix + strings.ToUpper(section+"_"+key),
				Doc:          kf.Tag.Get("doc"),
				Type:         typeName(v),
				InlineSecret: kf.Tag.Get("secret") == secretInline,
				SecretFile:   kf.Tag.Get("secret") == secretFile,
			})
		}
	}
	return out
}

func tomlName(sf reflect.StructField) string {
	if !sf.IsExported() {
		return ""
	}
	name, _, _ := strings.Cut(sf.Tag.Get("toml"), ",")
	if name == "-" {
		return ""
	}
	if name == "" {
		panic(fmt.Sprintf("config: field %s has no toml tag", sf.Name))
	}
	return name
}

var textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()

func typeName(v reflect.Value) string {
	if v.Type() == reflect.TypeFor[Duration]() {
		return "duration"
	}
	switch v.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "bool"
	case reflect.Int, reflect.Int64:
		return "int"
	case reflect.Float64:
		return "float"
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.String {
			return "list of strings"
		}
	default:
	}
	panic(fmt.Sprintf("config: unsupported field type %s", v.Type()))
}

// setFromString parses s according to the field's type and stores it. It is
// used for environment variables and CLI flags. Lists are comma-separated.
func (f Field) setFromString(s string) error {
	v := f.value
	if v.CanAddr() && v.Addr().Type().Implements(textUnmarshalerType) {
		tu, ok := v.Addr().Interface().(encoding.TextUnmarshaler)
		if !ok {
			return fmt.Errorf("%s: cannot parse value", f.Path)
		}
		if err := tu.UnmarshalText([]byte(s)); err != nil {
			return fmt.Errorf("%s: %w", f.Path, err)
		}
		return nil
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString(s)
	case reflect.Bool:
		b, err := strconv.ParseBool(strings.TrimSpace(s))
		if err != nil {
			return fmt.Errorf("%s: invalid bool %q", f.Path, s)
		}
		v.SetBool(b)
	case reflect.Int, reflect.Int64:
		n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			return fmt.Errorf("%s: invalid integer %q", f.Path, s)
		}
		v.SetInt(n)
	case reflect.Float64:
		n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return fmt.Errorf("%s: invalid number %q", f.Path, s)
		}
		v.SetFloat(n)
	case reflect.Slice:
		var items []string
		for item := range strings.SplitSeq(s, ",") {
			if item = strings.TrimSpace(item); item != "" {
				items = append(items, item)
			}
		}
		if items == nil {
			items = []string{}
		}
		v.Set(reflect.ValueOf(items))
	default:
		return fmt.Errorf("%s: unsupported type %s", f.Path, v.Type())
	}
	return nil
}
