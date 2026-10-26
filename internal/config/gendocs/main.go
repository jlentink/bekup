// Command gendocs regenerates the key reference in docs/configuration.md
// from the config structs. Run it from the repository root:
//
//	go run ./internal/config/gendocs
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/jlentink/bekup/internal/config"
)

const docPath = "docs/configuration.md"

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gendocs: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	raw, err := os.ReadFile(docPath)
	if err != nil {
		return err
	}
	doc := string(raw)
	before, rest, ok := strings.Cut(doc, config.ReferenceBegin)
	if !ok {
		return fmt.Errorf("%s: begin marker not found", docPath)
	}
	_, after, ok := strings.Cut(rest, config.ReferenceEnd)
	if !ok {
		return fmt.Errorf("%s: end marker not found", docPath)
	}
	var ref strings.Builder
	if err := config.WriteReference(&ref); err != nil {
		return err
	}
	out := before + config.ReferenceBegin + "\n" + ref.String() + "\n" + config.ReferenceEnd + after
	return os.WriteFile(docPath, []byte(out), 0o644) //nolint:gosec // G306: documentation file, world-readable on purpose
}
