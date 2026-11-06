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
	reader := envconfig.NewReader(nil)
	if _, err := provider.LoadConfig(reader); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	doc, err := Render(reader.Known())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		"| `LANTERN_PORT` |",
		"| `LANTERN_STRICT_CONFIG` |",
		"GENERATED FILE",
		"[SearchVertices contract](search.md)",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("rendered doc missing %q", want)
		}
	}
	live, retired, found := strings.Cut(doc, "\n## Retired authentication settings\n")
	if !found {
		t.Fatal("rendered doc missing separate retired authentication section")
	}
	for _, key := range []string{"LANTERN_AUTH_TOKENS", "LANTERN_AUTH_EXEMPT_REFLECTION"} {
		if strings.Contains(live, "| `"+key+"` |") {
			t.Errorf("retired setting %s appears in the live registry table", key)
		}
		if !strings.Contains(retired, "| `"+key+"` |") {
			t.Errorf("retired setting %s missing from documentation-only section", key)
		}
	}
	for _, want := range []string{"including an empty value", "both OFF and OIDC", "before configuration is published", "general strict mode is off"} {
		if !strings.Contains(retired, want) {
			t.Errorf("retired auth section missing presence-rejection guidance %q", want)
		}
	}
}

func TestRenderRejectsRetiredSettingsInLiveRegistry(t *testing.T) {
	reader := envconfig.NewReader(nil)
	if _, err := provider.LoadConfig(reader); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	for _, key := range []string{"LANTERN_AUTH_TOKENS", "LANTERN_AUTH_EXEMPT_REFLECTION"} {
		t.Run(key, func(t *testing.T) {
			specs := append(reader.Known(), envconfig.Spec{Key: key, Kind: "string"})
			if _, err := Render(specs); err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("Render accepted retired setting in live registry or failed to name it: %v", err)
			}
		})
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
