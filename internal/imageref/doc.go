// Package imageref parses container image references that are pinned by
// digest ([registry[:port]/]path[:tag]@sha256:<64 hex>). It holds the one
// image-reference grammar of bekup: internal/engine builds its catalog
// pattern (tag mandatory) from the same parts, and `bekup setup` (D-203)
// accepts only references ParsePinned accepts.
package imageref
