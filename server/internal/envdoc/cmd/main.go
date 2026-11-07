// Command envdoc regenerates the operator-facing environment-variable
// reference (docs/env.md) from the envconfig registry. Invoked by the
// generation directive in server/generate.go; see #847.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/internal/envdoc"
	"github.com/anaregdesign/lantern/server/provider"
)

func main() {
	out := flag.String("out", "", "output path (default: stdout)")
	flag.Parse()

	// An explicit empty snapshot renders defaults without changing the
	// invoking process environment or inheriting a previous load's findings.
	reader := envconfig.NewReader(nil)
	if _, err := provider.LoadConfig(reader); err != nil {
		fmt.Fprintln(os.Stderr, "envdoc: config load:", err)
		os.Exit(1)
	}
	doc, err := envdoc.Render(reader.Known())
	if err != nil {
		fmt.Fprintln(os.Stderr, "envdoc:", err)
		os.Exit(1)
	}
	if *out == "" {
		fmt.Print(doc)
		return
	}
	if err := os.WriteFile(*out, []byte(doc), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "envdoc:", err)
		os.Exit(1)
	}
}
