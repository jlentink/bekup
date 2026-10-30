// Package bekup embeds the public, non-secret deployment assets of the
// repository root into the binary, so that `bekup setup` (internal/setup,
// D-203) can write a complete compose deployment directory from the image
// alone, without a source checkout.
//
// Embedded (exactly what a compose deployment needs):
//
//   - docker-compose.yml, bekup.example.toml, runner.example.toml;
//   - deploy/postgres, deploy/traefik, deploy/nginx.
//
// Never embed anything secret, and never deploy/dev (dev keys, seeds).
// The setup tests assert that the embedded files are byte-equal to the
// repository files and that every ./deploy/... path the compose file mounts
// is embedded (and every embedded asset is used).
package bekup

import (
	"embed"
	"io/fs"
)

//go:embed docker-compose.yml bekup.example.toml runner.example.toml deploy/postgres deploy/traefik deploy/nginx
var assets embed.FS

// Assets returns the embedded deployment assets rooted at the repository
// root (e.g. "docker-compose.yml", "deploy/traefik/entrypoint.sh").
func Assets() fs.FS { return assets }
