package validate

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"regexp"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/targets"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/sshpolicy"
)

// Request and field limits.
const (
	// MaxRequestBytes bounds every unary request and stream header.
	MaxRequestBytes = 256 << 10
	// MaxDumpBytes bounds total_bytes of a verify/restore stream (64 TiB).
	MaxDumpBytes = 1 << 46
	// MaxPasswordBytes bounds database and SSH passwords and passphrases.
	MaxPasswordBytes = 1024
	// MaxAnchorBytes bounds one pinned certificate (DER).
	MaxAnchorBytes = 16 << 10
	// MaxClientCertBytes / MaxClientKeyBytes bound the client certificate
	// chain and key (PEM).
	MaxClientCertBytes = 32 << 10
	MaxClientKeyBytes  = 16 << 10
	// MaxSSHKeyBytes bounds the SSH private key (PEM).
	MaxSSHKeyBytes = 64 << 10
	// MaxSSHUserBytes bounds the SSH user name.
	MaxSSHUserBytes = sshpolicy.MaxUserLen
	// MaxAuthorizedKeyBytes bounds the pinned host key line.
	MaxAuthorizedKeyBytes = 8 << 10
	// MaxPins is the current pin plus one staged backup pin.
	MaxPins = 2
	// maxClientChain bounds the certificates in cert_pem.
	maxClientChain = 8
	// maxVersionKey bounds a catalog key.
	maxVersionKey = 16
)

var runIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Validator validates runner requests against the merged image catalog and
// the target allowlist. It is safe for concurrent use.
type Validator struct {
	cat   *engine.Catalog
	guard *targets.Guard
}

// New returns a Validator.
func New(cat *engine.Catalog, guard *targets.Guard) (*Validator, error) {
	if cat == nil || guard == nil {
		return nil, errors.New("validate: catalog and target guard are required")
	}
	return &Validator{cat: cat, guard: guard}, nil
}

// LoadCatalog returns the embedded catalog merged with runner.toml
// [images] extra_file (if set). The extra file is read with the config
// file-safety checks; any error fails startup.
func LoadCatalog(c *config.Runner) (*engine.Catalog, error) {
	cat, err := engine.Embedded()
	if err != nil {
		return nil, fmt.Errorf("image catalog: %w", err)
	}
	if c == nil || c.Images.ExtraFile == "" {
		return cat, nil
	}
	data, err := config.ReadPublicFile(c.Images.ExtraFile)
	if err != nil {
		return nil, fmt.Errorf("images.extra_file: %w", err)
	}
	merged, err := cat.WithExtra(data)
	if err != nil {
		return nil, fmt.Errorf("images.extra_file: %w", err)
	}
	return merged, nil
}

// Catalog returns the merged catalog.
func (v *Validator) Catalog() *engine.Catalog { return v.cat }

// Guard returns the target allowlist.
func (v *Validator) Guard() *targets.Guard { return v.guard }

// fieldError is an InvalidArgument status naming the field.
func fieldError(field, reason string) error {
	return status.Error(codes.InvalidArgument, "invalid request: "+field+": "+reason)
}

// checkEnvelope enforces the size limit and rejects unknown fields
// anywhere in the message.
func checkEnvelope(m proto.Message, limit int) error {
	if m == nil || !m.ProtoReflect().IsValid() {
		return fieldError("request", "missing")
	}
	if proto.Size(m) > limit {
		return fieldError("request", "too large")
	}
	if hasUnknown(m.ProtoReflect()) {
		return fieldError("request", "unknown fields")
	}
	return nil
}

func hasUnknown(r protoreflect.Message) bool {
	if len(r.GetUnknown()) > 0 {
		return true
	}
	found := false
	r.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.IsMap():
			if fd.MapValue().Message() != nil {
				v.Map().Range(func(_ protoreflect.MapKey, mv protoreflect.Value) bool {
					found = hasUnknown(mv.Message())
					return !found
				})
			}
		case fd.IsList():
			if fd.Message() != nil {
				l := v.List()
				for i := 0; i < l.Len() && !found; i++ {
					found = hasUnknown(l.Get(i).Message())
				}
			}
		case fd.Message() != nil:
			found = hasUnknown(v.Message())
		}
		return !found
	})
	return found
}

func checkRunContext(rc *runnerpb.RunContext) error {
	if rc == nil {
		return fieldError("ctx", "required")
	}
	if !runIDRe.MatchString(rc.GetRunId()) {
		return fieldError("ctx.run_id", "must be a lowercase UUID")
	}
	return nil
}

// kindOf maps the proto engine.
func kindOf(e runnerpb.Engine) (engine.Kind, bool) {
	switch e {
	case runnerpb.Engine_ENGINE_POSTGRES:
		return engine.KindPostgres, true
	case runnerpb.Engine_ENGINE_MYSQL:
		return engine.KindMySQL, true
	case runnerpb.Engine_ENGINE_MARIADB:
		return engine.KindMariaDB, true
	default:
		return "", false
	}
}

// entry resolves engine + version key against the merged allowlist.
func (v *Validator) entry(field string, e runnerpb.Engine, key string) (engine.Kind, engine.Engine, engine.Entry, error) {
	k, ok := kindOf(e)
	if !ok {
		return "", nil, engine.Entry{}, fieldError(field+"engine", "unknown or unspecified engine")
	}
	eng, err := engine.ForKind(k)
	if err != nil {
		return "", nil, engine.Entry{}, fieldError(field+"engine", "unknown engine")
	}
	if key == "" || len(key) > maxVersionKey {
		return "", nil, engine.Entry{}, fieldError(field+"version_key", "required")
	}
	ent, ok := v.cat.Lookup(k, key)
	if !ok {
		return "", nil, engine.Entry{}, fieldError(field+"version_key", "not in the runner's image allowlist")
	}
	return k, eng, ent, nil
}

// port converts and checks a proto port.
func port(field string, p uint32) (int, error) {
	if p < 1 || p > 65535 {
		return 0, fieldError(field, "must be 1-65535")
	}
	return int(p), nil
}

// allowlistError maps a target-guard error to a status.
func allowlistError(field string, err error) error {
	switch {
	case errors.Is(err, targets.ErrDenied):
		return status.Error(codes.PermissionDenied, field+": "+targets.ErrDenied.Error())
	case errors.Is(err, targets.ErrInvalidHost):
		return fieldError(field, "invalid host")
	default:
		return status.Error(codes.FailedPrecondition, field+": "+targets.ErrResolve.Error())
	}
}

// Endpoint is a host the runner connects to itself, resolved and checked
// against the target allowlist. The dial-time check (targets.Guard
// DialContext) still applies.
type Endpoint struct {
	Host  string
	Port  int
	Addrs []netip.Addr
}

// endpoint validates and resolves a host the runner dials directly.
func (v *Validator) endpoint(ctx context.Context, field, host string, p uint32) (Endpoint, error) {
	if err := engine.ValidateHost(host); err != nil {
		return Endpoint{}, fieldError(field+"host", "invalid host")
	}
	pt, err := port(field+"port", p)
	if err != nil {
		return Endpoint{}, err
	}
	addrs, err := v.guard.Resolve(ctx, host)
	if err != nil {
		return Endpoint{}, allowlistError(field+"host", err)
	}
	return Endpoint{Host: host, Port: pt, Addrs: addrs}, nil
}
