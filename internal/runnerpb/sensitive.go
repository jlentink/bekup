package runnerpb

import (
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// IsSecretField reports whether fd carries the [(runner.v1.secret) = true]
// option.
func IsSecretField(fd protoreflect.FieldDescriptor) bool {
	opts := fd.Options()
	if opts == nil {
		return false
	}
	v, ok := proto.GetExtension(opts, E_Secret).(bool)
	return ok && v
}

// mayHoldSecrets caches, per message type, whether the type or any nested
// message type has a secret field.
var mayHoldSecrets sync.Map // protoreflect.FullName -> bool

// HasSecretFields reports whether messages of type md can carry secret
// fields (directly or in nested messages). The result is static per type.
func HasSecretFields(md protoreflect.MessageDescriptor) bool {
	if v, ok := mayHoldSecrets.Load(md.FullName()); ok {
		b, ok := v.(bool)
		return ok && b
	}
	res := hasSecretFields(md, map[protoreflect.FullName]bool{})
	mayHoldSecrets.Store(md.FullName(), res)
	return res
}

func hasSecretFields(md protoreflect.MessageDescriptor, seen map[protoreflect.FullName]bool) bool {
	if seen[md.FullName()] {
		return false
	}
	seen[md.FullName()] = true
	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if IsSecretField(fd) {
			return true
		}
		if fd.IsMap() {
			if v := fd.MapValue(); v.Message() != nil && hasSecretFields(v.Message(), seen) {
				return true
			}
			continue
		}
		if fd.Message() != nil && hasSecretFields(fd.Message(), seen) {
			return true
		}
	}
	return false
}

// ZeroSecrets overwrites the backing bytes of every populated secret bytes
// field in m (recursively) with zeros and then clears the field; secret
// string fields are only cleared (Go strings cannot be overwritten, so the
// proto keeps credentials in bytes fields). Call it once a request or
// response is no longer needed. It is a no-op for nil messages.
//
// Limitation: the bytes are zeroed in the decoded message; copies the gRPC
// transport may still hold in its pooled receive buffers are outside our
// control.
func ZeroSecrets(m proto.Message) {
	if m == nil {
		return
	}
	r := m.ProtoReflect()
	if !r.IsValid() || !HasSecretFields(r.Descriptor()) {
		return
	}
	zeroMessage(r)
}

// ContainsSecrets reports whether m has at least one populated secret field
// (recursively). Unlike HasSecretFields it looks at the values, so a stream
// message that holds only a data chunk reports false.
func ContainsSecrets(m proto.Message) bool {
	if m == nil {
		return false
	}
	r := m.ProtoReflect()
	if !r.IsValid() || !HasSecretFields(r.Descriptor()) {
		return false
	}
	return containsSecrets(r)
}

func containsSecrets(r protoreflect.Message) bool {
	found := false
	r.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case IsSecretField(fd):
			found = true
		case fd.IsMap():
			if fd.MapValue().Message() != nil && HasSecretFields(fd.MapValue().Message()) {
				v.Map().Range(func(_ protoreflect.MapKey, mv protoreflect.Value) bool {
					found = containsSecrets(mv.Message())
					return !found
				})
			}
		case fd.IsList():
			if fd.Message() != nil && HasSecretFields(fd.Message()) {
				l := v.List()
				for i := 0; i < l.Len() && !found; i++ {
					found = containsSecrets(l.Get(i).Message())
				}
			}
		case fd.Message() != nil && HasSecretFields(fd.Message()):
			found = containsSecrets(v.Message())
		}
		return !found
	})
	return found
}

func zeroMessage(r protoreflect.Message) {
	r.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case IsSecretField(fd):
			zeroValue(fd, v)
			r.Clear(fd)
		case fd.IsMap():
			if fd.MapValue().Message() != nil {
				v.Map().Range(func(_ protoreflect.MapKey, mv protoreflect.Value) bool {
					zeroMessage(mv.Message())
					return true
				})
			}
		case fd.IsList():
			if fd.Message() != nil {
				l := v.List()
				for i := range l.Len() {
					zeroMessage(l.Get(i).Message())
				}
			}
		case fd.Message() != nil:
			zeroMessage(v.Message())
		}
		return true
	})
}

func zeroValue(fd protoreflect.FieldDescriptor, v protoreflect.Value) {
	if fd.Kind() != protoreflect.BytesKind {
		return
	}
	if fd.IsList() {
		l := v.List()
		for i := range l.Len() {
			clear(l.Get(i).Bytes())
		}
		return
	}
	clear(v.Bytes())
}
