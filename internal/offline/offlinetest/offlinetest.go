// Package offlinetest writes real backup runs (artifacts through the
// backup pipeline plus a signed manifest) onto a disk destination for the
// tests of the offline tooling (task 4.7). Test use only.
package offlinetest

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/offline"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/storage"
)

// DB is one database dump of a run.
type DB struct {
	Name   string
	Format pipeline.DumpFormat
	Data   []byte
}

// Spec describes a run to write.
type Spec struct {
	JobID, RunID string // default: random UUIDs
	Slug         string // default "app-db"
	Timestamp    time.Time
	Engine       pipeline.EngineInfo // default postgres 17
	Encryption   pipeline.Encryption
	DBs          []DB
	Globals      []byte // optional PostgreSQL globals dump
	Stats        []byte // optional canonical stats JSON
	Warnings     []string
	Key          *crypto.SigningKey // default: a new key
}

// Run is a written run.
type Run struct {
	Root     string // the disk destination root
	Dir      string // Root/{slug}/{timestamp}
	Key      *crypto.SigningKey
	Manifest *pipeline.Manifest
	File     []byte // manifest.json
	// Paths maps database names ("" for globals) to artifact files.
	Paths map[string]string
}

// ManifestPath is the path of manifest.json.
func (r *Run) ManifestPath() string { return filepath.Join(r.Dir, storage.ManifestFileName) }

// Write runs every dump through the pipeline onto a disk destination at
// root and signs the manifest.
func Write(t testing.TB, root string, s Spec) *Run {
	t.Helper()
	ctx := context.Background()
	if s.JobID == "" {
		s.JobID = uuid.NewString()
	}
	if s.RunID == "" {
		s.RunID = uuid.NewString()
	}
	if s.Slug == "" {
		s.Slug = "app-db"
	}
	if s.Timestamp.IsZero() {
		s.Timestamp = time.Date(2026, 10, 27, 3, 0, 0, 0, time.UTC)
	}
	if s.Engine.Kind == "" {
		s.Engine = pipeline.EngineInfo{Kind: "postgres", VersionKey: "17", ServerVersion: "17.6"}
	}
	if s.Key == nil {
		k, err := crypto.GenerateSigningKey()
		if err != nil {
			t.Fatal(err)
		}
		s.Key = k
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	disk, err := storage.OpenDisk(storage.DiskConfig{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	defer offline.CloseQuietly(disk)
	m := &pipeline.Manifest{
		FormatVersion: pipeline.ManifestFormatVersion, BekupVersion: "0.0.0-test", JobID: s.JobID, JobSlug: s.Slug,
		RunID: s.RunID, RunTimestamp: s.Timestamp, CreatedAt: s.Timestamp.Add(5 * time.Minute), Engine: s.Engine,
		Stats: s.Stats, Warnings: s.Warnings,
	}
	run := &Run{Root: root, Key: s.Key, Paths: map[string]string{}}
	write := func(kind pipeline.EntryKind, name string, format pipeline.DumpFormat, data []byte) {
		file, err := pipeline.ArtifactFileName(kind, name, format, s.Encryption.Mode)
		if err != nil {
			t.Fatal(err)
		}
		key, err := pipeline.ArtifactKey("", s.Slug, s.Timestamp, file)
		if err != nil {
			t.Fatal(err)
		}
		res, err := pipeline.Run(ctx, bytes.NewReader(data), pipeline.Spec{
			Encryption: s.Encryption,
			Targets:    []pipeline.Target{{Name: "disk", Dest: disk, Key: key}},
		})
		if err != nil {
			t.Fatal(err)
		}
		m.Entries = append(m.Entries, res.Entry(kind, name, file))
		run.Paths[name] = filepath.Join(root, filepath.FromSlash(key.String()))
	}
	for _, d := range s.DBs {
		write(pipeline.KindDatabase, d.Name, d.Format, d.Data)
	}
	if s.Globals != nil {
		write(pipeline.KindGlobals, "", pipeline.FormatSQL, s.Globals)
	}
	mkey, err := pipeline.ManifestKey("", s.Slug, s.Timestamp)
	if err != nil {
		t.Fatal(err)
	}
	wr, err := pipeline.WriteManifest(ctx, []pipeline.Target{{Name: "disk", Dest: disk, Key: mkey}}, s.Key, m)
	if err != nil {
		t.Fatal(err)
	}
	run.Manifest, run.File = m, wr.File
	run.Dir = filepath.Join(root, filepath.FromSlash(mkey.Dir().String()))
	return run
}

// WritePublicKey writes the key's public key file (bare base64) and
// returns its path.
func WritePublicKey(t testing.TB, dir string, k *crypto.SigningKey) string {
	t.Helper()
	p := filepath.Join(dir, "instance.pub")
	if err := os.WriteFile(p, []byte(PublicKeyBase64(k)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// PublicKeyBase64 is the standard base64 of the public key.
func PublicKeyBase64(k *crypto.SigningKey) string {
	return base64.StdEncoding.EncodeToString(k.Public)
}
