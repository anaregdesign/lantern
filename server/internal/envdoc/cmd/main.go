// Command envdoc regenerates the operator-facing environment-variable
// reference (docs/env.md) from the envconfig registry. Invoked by the
// go:generate directive in server/generate.go; see #847.
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

	// Scrub the process environment first so the captured registry reflects
	// pure defaults and the rendered file is deterministic regardless of the
	// invoking shell (a dev with LANTERN_* exported must produce the same
	// bytes CI does).
	os.Clearenv()
	if _, err := provider.NewConfig(); err != nil {
		fmt.Fprintln(os.Stderr, "envdoc: config load:", err)
		os.Exit(1)
	}
	doc, err := envdoc.Render(envconfig.Known())
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
