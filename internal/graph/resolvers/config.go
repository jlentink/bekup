package resolvers

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/configreload"
	"github.com/jlentink/bekup/internal/graph/model"
)

// Runtime config reload resolvers (task 6.13, D-200). They only
// translate: internal/configreload re-authorizes every call and returns
// key names and error kinds only.

var errConfigReloadNotConfigured = errors.New("resolvers: config reload API not configured")

func (r *Resolver) configReload() (*configreload.API, error) {
	if r.ConfigReload == nil {
		return nil, errConfigReloadNotConfigured
	}
	return r.ConfigReload, nil
}

func (r *Resolver) configStatus(ctx context.Context) (*model.ConfigStatus, error) {
	api, err := r.configReload()
	if err != nil {
		return nil, err
	}
	v, err := api.Status(ctx)
	if err != nil {
		return nil, err
	}
	out := &model.ConfigStatus{
		Database: databasePostureModel(v.Database), Image: imagePostureModel(v.Image),
		LiveKeys: nonNil(v.LiveKeys), Processes: processModels(v.Processes), ListeningProcesses: v.Listening,
	}
	if v.LastRequest != nil {
		out.LastRequest = reloadRequestModel(v.LastRequest)
	}
	return out, nil
}

func (r *Resolver) configReloadRequest(ctx context.Context, id uuid.UUID) (*model.ConfigReloadRequest, error) {
	api, err := r.configReload()
	if err != nil {
		return nil, err
	}
	v, err := api.RequestByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return reloadRequestModel(v), nil
}

func (r *Resolver) reloadConfig(ctx context.Context) (*model.ConfigReloadRequest, error) {
	api, err := r.configReload()
	if err != nil {
		return nil, err
	}
	v, err := api.Request(ctx)
	if err != nil {
		return nil, err
	}
	return reloadRequestModel(v), nil
}

func databasePostureModel(p configreload.DatabasePosture) *model.DatabasePosture {
	mode := model.DatabaseConfigModeURL
	switch p.Mode {
	case "fields":
		mode = model.DatabaseConfigModeFields
	case "single_account":
		mode = model.DatabaseConfigModeSingleAccount
	}
	return &model.DatabasePosture{Mode: mode, TLSMode: p.TLSMode, InsecureTransport: p.TLSWeakened, SingleAccount: p.SingleAccount}
}

func imagePostureModel(p configreload.ImagePosture) *model.ImagePosture {
	out := &model.ImagePosture{Following: p.Following, Version: p.Version}
	if p.SetupVersion != "" {
		v := p.SetupVersion
		out.SetupVersion = &v
	}
	return out
}

func reloadRequestModel(v *configreload.RequestView) *model.ConfigReloadRequest {
	return &model.ConfigReloadRequest{
		ID: v.ID, RequestedAt: v.RequestedAt, RequestedBy: v.RequestedBy,
		Trigger: model.ConfigReloadTrigger(strings.ToUpper(string(v.Trigger))),
		Results: processModels(v.Results), ListeningProcesses: v.Listening, ExpectedProcesses: v.Expected,
	}
}

func processModels(in []configreload.ProcessResult) []model.ConfigProcessResult {
	out := make([]model.ConfigProcessResult, len(in))
	for i := range in {
		p := &in[i]
		out[i] = model.ConfigProcessResult{
			InstanceID: p.InstanceID, Kind: model.BekupProcessKind(strings.ToUpper(string(p.Kind))),
			Hostname: p.Hostname, Version: p.Version, ProcessStartedAt: p.ProcessStartedAt, FinishedAt: p.FinishedAt,
			RequestID: p.RequestID, Listening: p.Listening, Status: model.ConfigReloadStatus(strings.ToUpper(string(p.Status))),
			AppliedKeys: nonNil(p.Applied), RestartKeys: nonNil(p.Restart),
			ErrorKeys: nonNil(p.ErrorKeys), WeakenedKeys: nonNil(p.Weakened),
		}
		if p.Trigger != "" {
			t := model.ConfigReloadTrigger(strings.ToUpper(string(p.Trigger)))
			out[i].Trigger = &t
		}
		if p.ErrorKind != "" {
			k := model.ConfigReloadErrorKind(strings.ToUpper(p.ErrorKind))
			out[i].ErrorKind = &k
		}
	}
	return out
}
