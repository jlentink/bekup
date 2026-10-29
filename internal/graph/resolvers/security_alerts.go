package resolvers

import (
	"context"

	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/notify"
)

// securityAlertTypes lists the security.system alert kinds (admin; the
// directive and the root-field gate check the role). Static data: no
// service is needed.
func (r *Resolver) securityAlertTypes(_ context.Context) ([]model.SecurityAlertType, error) {
	types := notify.SecurityAlertTypes()
	out := make([]model.SecurityAlertType, len(types))
	for i, t := range types {
		out[i] = model.SecurityAlertType{
			Kind: t.Kind, Severity: severityModel(string(t.Severity)), Description: t.Description, Repeat: t.Repeat,
		}
	}
	return out, nil
}
