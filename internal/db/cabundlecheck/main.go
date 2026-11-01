// Command cabundlecheck runs bekup's system CA bundle loader
// (db.LoadSystemCABundle, D-215) on a file and fails when it is missing or
// no certificate parses. The Dockerfile's build stage runs it on the
// runtime image's /etc/ssl/certs/ca-certificates.crt
// (config.SystemCABundle), so an image whose bundle the "verify with the
// system CA bundle" choice could not use is never built. Never shipped.
package main

import (
	"fmt"
	"os"

	"github.com/jlentink/bekup/internal/db"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: cabundlecheck <bundle file>")
		os.Exit(2)
	}
	_, st, err := db.LoadSystemCABundle(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "system CA bundle check failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("system CA bundle: %d certificates loaded, %d skipped\n", st.Loaded, st.Skipped)
}
