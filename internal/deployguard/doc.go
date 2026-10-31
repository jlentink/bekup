// Package deployguard holds the deployment facts `bekup setup` hands to
// migrate, serve, worker and runner through compose variables (D-212):
//
//   - BEKUP_SETUP_VERSION, the bekup version setup last prepared the
//     deployment directory for. `bekup migrate up`, `bekup db bootstrap`,
//     serve, worker and runner refuse to start when their own version
//     differs (upgrades and downgrades alike), so a bare `docker compose
//     pull && docker compose up -d` of a moved tag never runs new
//     migrations, deploy assets or a runner that `setup upgrade` has not
//     prepared. When the variable is unset
//     (deployments without setup, or set up before D-212) they only warn.
//   - BEKUP_IMAGE_MODE=following, set when the bekup image follows a tag
//     (ghcr.io/jlentink/bekup:latest) instead of a digest: an explicit
//     weakening that serve and worker warn about, audit
//     (security.unpinned_image) and the security check alerts daily. Outside
//     dev mode it requires BEKUP_SETUP_VERSION (Check). serve and worker
//     also treat the image as following when the runner reports that its
//     own container image carries no digest (Health image_pinned), so a
//     shell or .env override of this variable cannot hide the weakening.
//
// Versions are compared as exact strings (a leading "v" ignored): pre-
// release and build metadata count, so a development build (0.0.0-dev,
// 0.0.0+<commit>) never runs against a deployment set up by another
// build. The package reads nothing itself; callers pass the environment.
package deployguard
