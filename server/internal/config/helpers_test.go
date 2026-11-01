package config

import (
	"os"
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
)

func cleanEnvironment(t *testing.T) {
	t.Helper()
	for _, setting := range os.Environ() {
		key, _, _ := strings.Cut(setting, "=")
		if strings.HasPrefix(key, "LANTERN_") || strings.HasPrefix(key, "OTEL_") {
			t.Setenv(key, "")
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
	}
	envconfig.ResetForTesting()
	t.Cleanup(envconfig.ResetForTesting)
}
