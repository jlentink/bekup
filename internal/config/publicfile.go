package config

// maxPublicFileBytes bounds non-secret trust material such as certificates
// and CA bundles.
const maxPublicFileBytes = 1 << 20

// ReadPublicFile reads non-secret but integrity-relevant material (for
// example certificates and CA bundles) after enforcing the file safety rules
// of D-041: a regular file owned by the current user or root that is not
// group- or world-writable. Unlike ReadSecretFile it may be world-readable.
func ReadPublicFile(path string) ([]byte, error) {
	return readSafeFile(path, false, maxPublicFileBytes)
}
