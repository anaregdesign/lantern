package config

import (
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
)

func TestLoadLeavesEnvironmentFinalizationToComposition(t *testing.T) {
	cleanEnvironment(t)
	t.Setenv("LANTERN_STRICT_CONFIG", "true")
	t.Setenv("LANTERN_LATER_COMPONENT", "configured")
	cfg := Load(":9090")
	if err := cfg.Validate(); err != nil {
		t.Fatalf("ordinary value validation: %v", err)
	}
	// Loading another component must complete before the global typo sweep.
	if got := envconfig.String("LANTERN_LATER_COMPONENT", ""); got != "configured" {
		t.Fatalf("later component value = %q", got)
	}
	if err := CheckEnvironment(os.Environ(), slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("completed environment registry: %v", err)
	}
}
