package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/instancekeys"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/runstate"
)

// InstanceKeySigner provides the active instance signing key for manifests
// (D-087, D-144), creating (and auditing) it on first use like the audit
// head signer.
type InstanceKeySigner struct {
	DB      sqlc.DBTX
	Keyring *crypto.Keyring
	Auditor *audit.Auditor
}

// SigningKey implements ManifestSigner. The caller destroys the key.
func (s InstanceKeySigner) SigningKey(ctx context.Context) (*crypto.SigningKey, error) {
	key, created, err := instancekeys.Active(ctx, sqlc.New(s.DB), s.Keyring)
	if err != nil {
		return nil, err
	}
	if created {
		if _, err := s.Auditor.Record(ctx, audit.Event{
			Actor: audit.SystemActor(), Action: audit.ActionInstanceKeyCreated,
			TargetType: "instance_key", TargetID: key.ID,
			Details: audit.Details{"fingerprint": crypto.FingerprintBase64(key.Public), "purpose": pipeline.ManifestSignaturePurpose},
		}); err != nil {
			key.Destroy()
			return nil, err
		}
	}
	return key, nil
}

// writeManifest signs the manifest of the run's complete items and puts it
// on every destination that holds all of them (D-144). Destinations
// without a manifest make the run partial.
func (e *runExec) writeManifest(ctx context.Context) {
	var entries []pipeline.Entry
	var complete []*itemState
	for _, it := range e.items {
		if it.entry != nil {
			entries = append(entries, *it.entry)
			complete = append(complete, it)
		}
	}
	if len(entries) == 0 {
		return
	}
	key, err := pipeline.ManifestKey("", e.job.Slug, e.ts)
	if err != nil {
		e.manifestProblem("", "no manifest key could be derived")
		return
	}
	var targets []pipeline.Target
	// signed: the artifact ids each manifest lists, per destination (the
	// only objects MarkSigned may sign there).
	signed := map[string][]uuid.UUID{}
	for _, d := range e.dests {
		if d.d == nil {
			continue
		}
		all := true
		var ids []uuid.UUID
		for _, it := range complete {
			id, ok := it.okDests[d.id.String()]
			if !ok {
				all = false
				break
			}
			ids = append(ids, id)
		}
		if all {
			targets = append(targets, pipeline.Target{Name: d.id.String(), Dest: d.d, Key: key})
			signed[d.id.String()] = ids
		} else {
			e.manifestProblem(d.id.String(), "the destination does not hold every artifact of the run, so it gets no manifest")
		}
	}
	if len(targets) == 0 {
		return
	}
	m := &pipeline.Manifest{
		FormatVersion: pipeline.ManifestFormatVersion,
		BekupVersion:  e.o.BekupVersion,
		JobID:         e.job.ID.String(),
		JobSlug:       e.job.Slug,
		RunID:         e.run.RunID.String(),
		RunTimestamp:  e.ts,
		CreatedAt:     e.o.Now().UTC(),
		Engine:        pipeline.EngineInfo{Kind: string(e.job.Engine), VersionKey: e.job.VersionKey, ServerVersion: e.server},
		Entries:       entries,
		Stats:         manifestStats(complete, e.excluded),
		Warnings:      manifestWarnings(e.allWarnings()),
	}
	sk, err := e.o.Signer.SigningKey(ctx)
	if err != nil {
		e.log.ErrorContext(ctx, "loading the instance signing key failed", slog.Any("error", err))
		e.manifestProblem("", "the instance signing key is unavailable; no manifest was written")
		return
	}
	defer sk.Destroy()
	res, err := pipeline.WriteManifest(ctx, targets, sk, m)
	if err != nil && len(res.Destinations) == 0 {
		e.log.ErrorContext(ctx, "writing the manifest failed", slog.Any("error", err))
		e.manifestProblem("", "the manifest could not be built")
		return
	}
	for i := range res.Destinations {
		d := &res.Destinations[i]
		ds := e.dest(d.Name)
		if ds == nil {
			continue
		}
		if d.Outcome != pipeline.OutcomeOK {
			e.log.WarnContext(ctx, "writing the manifest to a destination failed", slog.String("destination_id", ds.id.String()),
				slog.String("code", string(d.Code)), slog.Any("error", d.Err))
			e.manifestProblem(ds.id.String(), "manifest.json could not be stored: "+d.Code.Message())
			e.reportDestination(ctx, ds.id, d.Code)
			continue
		}
		if err := e.o.Store.MarkSigned(ctx, e.run.Ref, runstate.Artifact{
			DestinationID: ds.id, Kind: sqlc.ArtifactKindManifest, Key: d.Key.String(), Size: d.Info.Size,
			SHA256: res.SHA256, VersionID: d.Info.VersionID, SigningKeyID: sk.ID, LockUntil: d.Info.RetainUntil,
		}, signed[ds.id.String()]); err != nil {
			e.log.ErrorContext(ctx, "recording the manifest failed", slog.Any("error", err))
			e.manifestProblem(ds.id.String(), "the manifest was stored but could not be recorded")
			continue
		}
		ds.manifest = true
	}
	e.logs.Logf(runstate.LevelInfo, "signed manifest (key %s) written to %d destination(s)", sk.ID, countManifests(e.dests))
}

func countManifests(ds []*destState) int {
	n := 0
	for _, d := range ds {
		if d.manifest {
			n++
		}
	}
	return n
}

func (e *runExec) manifestProblem(dest, msg string) {
	e.warnings = append(e.warnings, runstate.Warning{Code: CodeManifestFailed, Message: msg, DestinationID: dest})
	e.logs.Logf(runstate.LevelWarn, "manifest: %s", msg)
}

// maxManifestStatsBytes keeps the stats well inside the manifest limit
// (pipeline.MaxManifestBytes): beyond it the per-table lists are dropped
// (object counts and warnings stay, "tables_omitted":1 marks it), so a run
// with many large databases still gets a manifest.
const maxManifestStatsBytes = 8 << 20

// manifestStats is {"databases": {<enc db>: stats}, "globals": stats,
// "databases_excluded": [<enc db>, ...]} of the complete items with stats
// and the databases a mode-all job excluded on purpose (nil when there is
// nothing).
func manifestStats(items []*itemState, excluded []string) json.RawMessage {
	c := buildManifestStats(items, excluded, false)
	if c == nil || len(c) > maxManifestStatsBytes {
		// Too large (or over the canonical size limit): drop the tables.
		c = buildManifestStats(items, excluded, true)
	}
	if len(c) > maxManifestStatsBytes {
		return nil
	}
	return c
}

// maxExcludedNames bounds the excluded database names in the manifest.
const maxExcludedNames = 1000

func buildManifestStats(items []*itemState, excluded []string, omitTables bool) json.RawMessage {
	dbs := map[string]json.RawMessage{}
	out := map[string]any{}
	for _, it := range items {
		if len(it.stats) == 0 {
			continue
		}
		st := it.stats
		if omitTables {
			st = withoutTables(st)
		}
		if it.kind == pipeline.KindGlobals {
			out["globals"] = st
		} else {
			dbs[pipeline.EncodeName(it.db)] = st
		}
	}
	if len(dbs) > 0 {
		out["databases"] = dbs
	}
	if len(excluded) > 0 {
		names := make([]string, 0, min(len(excluded), maxExcludedNames))
		for _, d := range excluded[:min(len(excluded), maxExcludedNames)] {
			names = append(names, pipeline.EncodeName(d))
		}
		out["databases_excluded"] = names
	}
	if len(out) == 0 {
		return nil
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil
	}
	c, err := pipeline.Canonicalize(b)
	if err != nil {
		return nil
	}
	return c
}

// manifestWarnings formats the run's warnings for the manifest (bounded).
func manifestWarnings(ws []runstate.Warning) []string {
	var out []string
	for _, w := range ws {
		if len(out) == pipeline.MaxWarnings {
			break
		}
		s := w.Code + ": " + w.Message
		if w.Database != "" {
			s += fmt.Sprintf(" (database %s)", pipeline.EncodeName(w.Database))
		}
		if w.DestinationID != "" {
			s += " (destination " + w.DestinationID + ")"
		}
		if len(s) > pipeline.MaxWarningLen {
			cut := pipeline.MaxWarningLen
			for cut > 0 && !utf8.RuneStart(s[cut]) {
				cut--
			}
			s = s[:cut]
		}
		out = append(out, s)
	}
	return out
}

// withoutTables drops the per-table list of a canonical stats object.
func withoutTables(st json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(st, &m); err != nil {
		return st
	}
	if _, ok := m["tables"]; !ok {
		return st
	}
	delete(m, "tables")
	m["tables_omitted"] = json.RawMessage("1")
	b, err := json.Marshal(m)
	if err != nil {
		return st
	}
	return b
}
