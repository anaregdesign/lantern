package config

import (
	"bytes"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestCheckEnvironment(t *testing.T) {
	for _, strict := range []bool{false, true} {
		name := "warnings"
		if strict {
			name = "strict refusal"
		}
		t.Run(name, func(t *testing.T) {
			cleanEnvironment(t)
			t.Setenv("LANTERN_STRICT_CONFIG", strconv.FormatBool(strict))
			t.Setenv("LANTERN_PORT", "not-a-number")
			t.Setenv("LANTERN_PROT", "do-not-log-this-value")
			cfg := Load(":9090")
			if cfg.Net.Port != 6380 {
				t.Fatalf("malformed port = %d, want default", cfg.Net.Port)
			}
			var output bytes.Buffer
			err := CheckEnvironment(os.Environ(), slog.New(slog.NewJSONHandler(&output, nil)))
			if (err != nil) != strict {
				t.Fatalf("CheckEnvironment strict=%t: %v", strict, err)
			}
			for _, want := range []string{"malformed value, using default", "LANTERN_PORT", "LANTERN_PROT", "did_you_mean", "environment overrides active"} {
				if !strings.Contains(output.String(), want) {
					t.Fatalf("diagnostics missing %q: %s", want, output.String())
				}
			}
			if strings.Contains(output.String(), "do-not-log-this-value") || (err != nil && strings.Contains(err.Error(), "do-not-log-this-value")) {
				t.Fatalf("unknown variable value leaked in diagnostics: %v; %s", err, output.String())
			}
			if strict && (!strings.Contains(err.Error(), "LANTERN_PORT") || !strings.Contains(err.Error(), "LANTERN_PROT")) {
				t.Fatalf("strict error omits finding keys: %v", err)
			}
		})
	}
	t.Run("foreign namespaces", func(t *testing.T) {
		cleanEnvironment(t)
		t.Setenv("LANTERN_STRICT_CONFIG", "true")
		t.Setenv("LANTERN_MCP_AGENT_ID", "agent")
		t.Setenv("LANTERN_TOKEN", "client-secret")
		Load(":9090")
		var output bytes.Buffer
		if err := CheckEnvironment(os.Environ(), slog.New(slog.NewJSONHandler(&output, nil))); err != nil {
			t.Fatalf("foreign settings rejected: %v", err)
		}
		if strings.Contains(output.String(), "client-secret") {
			t.Fatal("foreign client token leaked")
		}
	})
}
