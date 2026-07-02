package envdoc

import (
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/provider"
)

// TestRenderCoversFullRegistry loads the real config so the envconfig
// registry holds the server's complete env contract, then requires Render to
// succeed — i.e. every registered variable has a description and no
// description is stale. This is the drift gate: adding a LANTERN_* variable
// without documenting it (or removing one and leaving its row) fails here
// and in `go generate` (#847).
func TestRenderCoversFullRegistry(t *testing.T) {
	envconfig.ResetForTesting()
	// Strict mode aborts NewConfig on any pre-existing junk in the test
	// host's environment; force it off so this test only checks coverage.
	t.Setenv("LANTERN_STRICT_CONFIG", "false")
	if _, err := provider.NewConfig(); err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	doc, err := Render(envconfig.Known())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{"| `LANTERN_PORT` |", "| `LANTERN_STRICT_CONFIG` |", "GENERATED FILE"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("rendered doc missing %q", want)
		}
	}
}

// TestRenderRejectsDrift feeds Render a registry that disagrees with the
// description table in both directions and expects a descriptive error.
func TestRenderRejectsDrift(t *testing.T) {
	_, err := Render([]envconfig.Spec{{Key: "LANTERN_NOT_DOCUMENTED", Kind: "int", Default: "1"}})
	if err == nil {
		t.Fatal("Render accepted a registry/description mismatch")
	}
	if !strings.Contains(err.Error(), "LANTERN_NOT_DOCUMENTED") {
		t.Fatalf("error does not name the undocumented variable: %v", err)
	}
	// Every curated description is stale relative to this one-entry registry,
	// so the stale side must be reported too.
	if !strings.Contains(err.Error(), "LANTERN_PORT") {
		t.Fatalf("error does not name stale descriptions: %v", err)
	}
}
