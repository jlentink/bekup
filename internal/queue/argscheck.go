package queue

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

// ErrArgsNotIDsOnly is returned by CheckArgs for args that could carry
// secrets or free-form data.
var ErrArgsNotIDsOnly = errors.New("job args must carry IDs only")

// secretName matches field and JSON names that suggest secret material.
var secretName = regexp.MustCompile(`(?i)pass(word|phrase)|pwd|key|token|secret|identit|credential|private|cookie|dsn`)

var (
	uuidType = reflect.TypeFor[uuid.UUID]()
	timeType = reflect.TypeFor[time.Time]()
)

// CheckArgs enforces "job args never contain secrets" (D-043) structurally:
// args are a struct whose fields (recursively) are only uuid.UUID,
// time.Time, bool, integers, named string types (enums), or pointers and
// slices of those. Plain strings, byte slices, maps, interfaces, floats and
// any field whose Go or JSON name looks secret-bearing are rejected. River
// stores args in plain JSON in river_job and they may surface in logs and
// admin tools; secrets travel only through the encrypted tables.
func CheckArgs(args river.JobArgs) error {
	t := reflect.TypeOf(args)
	if t == nil {
		return fmt.Errorf("%w: nil args", ErrArgsNotIDsOnly)
	}
	if t.Kind() != reflect.Struct {
		return fmt.Errorf("%w: %s is not a struct", ErrArgsNotIDsOnly, t)
	}
	return checkStruct(t, t.Name(), 0)
}

func checkStruct(t reflect.Type, path string, depth int) error {
	if depth > 4 {
		return fmt.Errorf("%w: %s nests too deeply", ErrArgsNotIDsOnly, path)
	}
	for f := range t.Fields() {
		p := path + "." + f.Name
		if !f.IsExported() {
			return fmt.Errorf("%w: %s is unexported", ErrArgsNotIDsOnly, p)
		}
		jsonName, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if secretName.MatchString(f.Name) || secretName.MatchString(jsonName) {
			return fmt.Errorf("%w: %s has a secret-looking name", ErrArgsNotIDsOnly, p)
		}
		if err := checkType(f.Type, p, depth); err != nil {
			return err
		}
	}
	return nil
}

func checkType(t reflect.Type, path string, depth int) error {
	if t == uuidType || t == timeType {
		return nil
	}
	switch t.Kind() {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return nil
	case reflect.String:
		if t.Name() == "" || t.PkgPath() == "" {
			return fmt.Errorf("%w: %s is a plain string (use a uuid or a named enum type)", ErrArgsNotIDsOnly, path)
		}
		return nil
	case reflect.Pointer:
		return checkType(t.Elem(), path, depth)
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return fmt.Errorf("%w: %s is a byte slice", ErrArgsNotIDsOnly, path)
		}
		return checkType(t.Elem(), path+"[]", depth)
	case reflect.Struct:
		return checkStruct(t, path, depth+1)
	default:
		return fmt.Errorf("%w: %s has disallowed type %s", ErrArgsNotIDsOnly, path, t)
	}
}
