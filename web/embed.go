// Package web embeds the built single-page UI (Vite output in web/dist) into
// the bekup binary. `bekup serve` serves it with the SPA fallback and the
// security headers from internal/httpserver (task 1.1).
//
// Before the first `pnpm build`, dist contains only .gitkeep so that this
// package (and `go build ./...`) always compiles; the UI is then empty.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// DistFS returns the built UI rooted at the dist directory (index.html at the
// root). It contains only static, public assets: never embed secrets here.
func DistFS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		// fs.Sub only fails for an invalid path; "dist" is a constant.
		panic("web: invalid embedded dist path: " + err.Error())
	}
	return sub
}

// HasIndex reports whether a built index.html is embedded (false before the
// first web build).
func HasIndex() bool {
	_, err := fs.Stat(DistFS(), "index.html")
	return err == nil
}
