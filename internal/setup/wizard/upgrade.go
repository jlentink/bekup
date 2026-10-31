package wizard

import (
	"fmt"
	"strings"

	"github.com/jlentink/bekup/internal/setup"
)

// UpgradeConfirm is the guided part of `bekup setup upgrade`: the Confirm
// hook of setup.Upgrade shows the versions, the compose changes, new secret
// groups and the restart, reminds of the backup and asks before anything
// is written.
func UpgradeConfirm(ui UI) func(*setup.UpgradeSummary) error {
	return func(s *setup.UpgradeSummary) error {
		var b strings.Builder
		fmt.Fprintf(&b, "Version: %s -> %s\nImage:   %s\n     ->  %s\n", s.OldVersion, s.NewVersion, s.OldImage, s.NewImage)
		db := "bundled database"
		if s.DB == setup.DBExternal {
			db = "your own PostgreSQL"
		}
		fmt.Fprintf(&b, "Variant: %s with %s\n", s.Variant, db)
		if s.ComposeMode == setup.ComposeOperator {
			fmt.Fprintf(&b, "compose.yaml: your release file (%s); setup never writes it\n", s.ComposeLevel)
		} else {
			fmt.Fprintf(&b, "compose.yaml: rewritten (%s)\n", diffSummary(s.ComposeDiff))
		}
		fmt.Fprintf(&b, "Generated files: %d change, %d removed", len(s.Changed), len(s.Removed))
		if len(s.BackedUp) > 0 {
			fmt.Fprintf(&b, ", %d of yours backed up first (.bak-<time>)", len(s.BackedUp))
		}
		b.WriteString("\n")
		if len(s.NewSecretGroups) > 0 {
			fmt.Fprintf(&b, "New secrets: %s\n", strings.Join(s.NewSecretGroups, ", "))
		}
		if s.EnvImage {
			b.WriteString(".env: BEKUP_IMAGE is updated\n")
		}
		for _, f := range s.Findings {
			fmt.Fprintf(&b, "%s\n", f)
		}
		b.WriteString("Restart: every service (docker compose pull && docker compose up -d);\nbekup.toml and runner.toml are re-validated, never rewritten.")
		if err := ui.Note("Upgrade", b.String()); err != nil {
			return err
		}
		if s.ComposeDiff != "" {
			if err := ui.Note("compose.yaml changes", firstLines(s.ComposeDiff, 16)); err != nil {
				return err
			}
		}
		ok, err := ui.Confirm("Backup", "Upgrades migrate the database schema. Do you have a current backup of\nbekup's database and of the secrets directory (docs/installation/upgrades.md)?", false)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: back up first (docs/installation/upgrades.md)", ErrAborted)
		}
		ok, err = ui.Confirm("Upgrade now?", "Writes the generated files; compose.yaml only when setup owns it.", true)
		if err != nil {
			return err
		}
		if !ok {
			return ErrAborted
		}
		return nil
	}
}

// diffSummary counts the added and removed lines of a LineDiff.
func diffSummary(d string) string {
	if d == "" {
		return "no changes"
	}
	add, del := 0, 0
	for _, l := range strings.Split(d, "\n") {
		switch {
		case strings.HasPrefix(l, "  + "):
			add++
		case strings.HasPrefix(l, "  - "):
			del++
		}
	}
	return fmt.Sprintf("+%d -%d lines", add, del)
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = append(lines[:n], fmt.Sprintf("... %d more", len(lines)-n))
	}
	return strings.Join(lines, "\n")
}
