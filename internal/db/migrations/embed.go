// Package migrations embeds the goose SQL migrations of the bekup app DB
// (NNNNN_description.sql with -- +goose Up/Down). Create new files with
// `just migrate-new <name>`; never edit a released migration.
package migrations

import "embed"

// FS holds every migration file.
//
//go:embed *.sql
var FS embed.FS
