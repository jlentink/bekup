package runner

import (
	"context"

	"github.com/jlentink/bekup/internal/runnerpb"
)

// FetchServerCertificate captures a target's TLS certificate chain so an
// admin can confirm a pin (task 2.7a; ops_cert.go).
func (s *service) FetchServerCertificate(ctx context.Context, req *runnerpb.FetchServerCertificateRequest) (*runnerpb.FetchServerCertificateResponse, error) {
	defer runnerpb.ZeroSecrets(req)
	if s.ops == nil {
		return nil, errOpsUnavailable
	}
	return s.ops.fetchServerCertificate(ctx, req)
}
