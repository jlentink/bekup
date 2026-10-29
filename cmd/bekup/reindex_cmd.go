package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/offline"
	"github.com/jlentink/bekup/internal/storage"
)

type reindexFlags struct {
	destConfig    string
	jobSlug       string
	pubkeys       []string
	skipArtifacts bool
	json          bool
	doImport      bool
	destinationID string
	dryRun        bool
}

func newReindexCmd(g *globalFlags) *cobra.Command {
	f := &reindexFlags{}
	cmd := &cobra.Command{
		Use:   "reindex --destination-config <toml> [--job-slug <slug>] (--pubkey <k> | --import --destination-id <id>)",
		Short: "List and verify the runs on a destination; with --import, record them for a job after a database loss",
		Long: "Lists a destination (disk, S3 or SFTP, described by an offline TOML file with *_file credentials,\n" +
			"see docs/recovery.md), groups its objects into runs, verifies each run's signed manifest (location,\n" +
			"signature) and streams every artifact to check its size and SHA-256. The destination is opened\n" +
			"read-only (no-delete mode) and never written.\n\n" +
			"Without --import it needs no database and prints an inventory (verified with --pubkey).\n" +
			"With --import (needs the bekup config and database) the runs of --job-slug are verified against\n" +
			"the database's instance keys and recorded as runs and artifacts of that job on --destination-id,\n" +
			"so the restore UI can use them. Only runs whose signature verifies are imported; artifacts are\n" +
			"marked verified only when they match the signed manifest. Runs already in the database are\n" +
			"skipped. Every import is recorded in the audit log (cli.run.imported). --dry-run shows what\n" +
			"would be imported. Retention applies to imported runs like to any other run of the job.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error { return runReindex(c, g, f) },
	}
	fl := cmd.Flags()
	fl.StringVar(&f.destConfig, "destination-config", "", "offline destination description (TOML, see docs/recovery.md) (required)")
	fl.StringVar(&f.jobSlug, "job-slug", "", "only the runs of this job (required with --import)")
	addPubkeyFlag(cmd, &f.pubkeys)
	fl.BoolVar(&f.skipArtifacts, "skip-artifacts", false, "verify only the manifests, do not read the artifacts (not with --import)")
	fl.BoolVar(&f.json, "json", false, "print the inventory as JSON")
	fl.BoolVar(&f.doImport, "import", false, "import the verified runs into the bekup database (needs the bekup config)")
	fl.StringVar(&f.destinationID, "destination-id", "", "with --import: the bekup destination (UUID) these objects live on")
	fl.BoolVar(&f.dryRun, "dry-run", false, "with --import: verify and show what would be imported, write nothing")
	mustRequire(cmd, "destination-config")
	return cmd
}

func (f *reindexFlags) validate() error {
	switch {
	case f.dryRun && !f.doImport:
		return errors.New("--dry-run only applies to --import (the inventory never writes anything)")
	case f.doImport && len(f.pubkeys) > 0:
		return errors.New("--import verifies against the database's instance keys: omit --pubkey")
	case f.doImport && f.jobSlug == "":
		return errors.New("--import needs --job-slug")
	case f.doImport && f.destinationID == "":
		return errors.New("--import needs --destination-id (the bekup destination these objects live on)")
	case f.doImport && f.skipArtifacts:
		return errors.New("--import always checks the artifacts: omit --skip-artifacts")
	case !f.doImport && f.destinationID != "":
		return errors.New("--destination-id only applies to --import")
	case !f.doImport && len(f.pubkeys) == 0:
		return errors.New("--pubkey is required to verify the manifests (or use --import with the bekup database)")
	}
	if f.jobSlug != "" {
		if err := storage.ValidateJobSlug(f.jobSlug); err != nil {
			return fmt.Errorf("--job-slug: %w", err)
		}
	}
	return nil
}

type reindexRunJSON struct {
	Dir      string          `json:"dir"`
	Objects  int             `json:"objects"`
	Verified bool            `json:"verified"`
	Error    string          `json:"error,omitempty"`
	Report   *offline.Report `json:"report,omitempty"`
	Import   string          `json:"import,omitempty"`
}

func runReindex(c *cobra.Command, g *globalFlags, f *reindexFlags) error {
	if err := f.validate(); err != nil {
		return err
	}
	var destID uuid.UUID
	if f.doImport {
		id, err := uuid.Parse(f.destinationID)
		if err != nil {
			return errors.New("--destination-id must be a UUID")
		}
		destID = id
	}
	dc, err := offline.LoadDestinationConfig(f.destConfig)
	if err != nil {
		return err
	}
	ctx := c.Context()
	keys := &offline.KeySet{}
	var importer *offline.Importer
	if f.doImport {
		pool, _, _, closePool, err := openAppDB(c, g, "bekup-reindex")
		if err != nil {
			return err
		}
		defer closePool()
		dbKeys, err := offline.DBPublicKeys(ctx, sqlc.New(pool))
		if err != nil {
			return err
		}
		if len(dbKeys) == 0 {
			return errors.New("the database has no instance keys: nothing can be verified for an import")
		}
		keys.Add(dbKeys...)
		importer = &offline.Importer{DB: pool, Auditor: newCLIAuditor(pool), DestinationID: destID, DestinationType: dc.Type}
	} else {
		ks, err := loadKeys(f.pubkeys)
		if err != nil {
			return err
		}
		keys = ks
	}
	dest, err := dc.Open(ctx, nil)
	if err != nil {
		return fmt.Errorf("open destination: %w", err)
	}
	defer offline.CloseQuietly(dest)
	runs, err := offline.Scan(ctx, dest, offline.ScanOptions{
		JobSlug: f.jobSlug, Keys: keys.Verifier(), KeySet: keys, SkipArtifacts: f.skipArtifacts,
	})
	if err != nil {
		return err
	}
	w := c.OutOrStdout()
	failed := 0
	results := make([]reindexRunJSON, 0, len(runs))
	for i := range runs {
		r := &runs[i]
		item := reindexRunJSON{Dir: r.Dir.String(), Objects: r.Objects, Verified: r.Verified(), Report: r.Report}
		if r.Err != nil {
			item.Error = offline.SafeText(r.Err.Error())
			if !errors.Is(r.Err, offline.ErrIncomplete) {
				failed++
			}
		} else if r.Report != nil && !r.Report.OK {
			failed++
		}
		if importer != nil {
			item.Import = importRun(c, importer, r, f.dryRun, &failed)
		}
		results = append(results, item)
	}
	if f.json {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(results); err != nil {
			return err
		}
	} else if err := writeInventory(w, dc.Type, f, results); err != nil {
		return err
	}
	if failed > 0 {
		return fmt.Errorf("%d run(s) failed verification or import", failed)
	}
	return nil
}

func importRun(c *cobra.Command, im *offline.Importer, r *offline.ScannedRun, dryRun bool, failed *int) string {
	switch {
	case !r.Verified():
		// Already counted as a verification failure (or an incomplete run).
		return "not imported: the run did not verify completely (bekup decrypt can still read its intact artifacts)"
	case dryRun:
		return fmt.Sprintf("would import (%d artifact(s), all verified)", len(r.Report.Entries))
	}
	res, err := im.Import(c.Context(), r)
	switch {
	case errors.Is(err, offline.ErrAlreadyIndexed):
		return "skipped: " + offline.SafeText(err.Error())
	case err != nil:
		*failed++
		return "FAILED: " + offline.SafeText(err.Error())
	}
	return fmt.Sprintf("imported run %s with %d verified artifact(s); recorded in the audit log", res.RunID, res.Artifacts)
}

func writeInventory(w io.Writer, typ string, f *reindexFlags, results []reindexRunJSON) error {
	var b strings.Builder
	scope := "all jobs"
	if f.jobSlug != "" {
		scope = "job " + f.jobSlug
	}
	fmt.Fprintf(&b, "destination (%s), %s: %d run director(y/ies)\n", typ, scope, len(results))
	for _, r := range results {
		state := "VERIFIED"
		switch {
		case r.Error != "":
			state = "NOT VERIFIED"
		case r.Report != nil && r.Report.Partial:
			state = "MANIFEST VERIFIED (artifacts not read)"
		case r.Report != nil && !r.Report.OK:
			state = "FAILED"
		}
		fmt.Fprintf(&b, "\n%s  [%s]  %d object(s)\n", r.Dir, state, r.Objects)
		if r.Error != "" {
			fmt.Fprintf(&b, "  reason: %s\n", r.Error)
		}
		if rep := r.Report; rep != nil {
			m := rep.Manifest
			var stored, raw int64
			for i := range rep.Entries {
				stored += rep.Entries[i].Entry.StoredSize
				raw += rep.Entries[i].Entry.RawSize
			}
			fmt.Fprintf(&b, "  job %s (id %s), run %s, signed %s by key %s %s\n", m.JobSlug, m.JobID, m.RunID,
				m.CreatedAt.Format(time.RFC3339), rep.KeyID, rep.KeyFingerprint)
			fmt.Fprintf(&b, "  engine %s %s (server %s); %d artifact(s), %s stored, %s raw\n", m.Engine.Kind, m.Engine.VersionKey,
				offline.SafeText(m.Engine.ServerVersion), len(rep.Entries), offline.HumanBytes(stored), offline.HumanBytes(raw))
			for i := range rep.Entries {
				e := &rep.Entries[i]
				if e.Status == offline.StatusOK || e.Status == offline.StatusSkipped {
					continue
				}
				fmt.Fprintf(&b, "  [%s] %s %s: %s\n", strings.ToUpper(string(e.Status)), e.Entry.Kind, e.Name, e.Detail)
			}
			if len(rep.Entries) > 0 {
				fmt.Fprintf(&b, "  encryption: %s\n", offline.EncryptionText(rep.Entries[0].Entry.Encryption))
			}
			if n := len(m.Warnings); n > 0 {
				fmt.Fprintf(&b, "  %d run warning(s) (bekup verify shows them)\n", n)
			}
		}
		if r.Import != "" {
			fmt.Fprintf(&b, "  import: %s\n", r.Import)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}
