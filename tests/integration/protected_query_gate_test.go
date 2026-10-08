package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This explicit short lane runs the actual production binary, local issuer,
// fixture supervisor and driver. It seeds/preflights, and performs no load or
// performance qualification. Build all three binaries from the same checkout.
func TestProtectedQueryPreparation_ProductionWire(t *testing.T) {
	server := os.Getenv("LANTERN_PROTECTED_QUERY_SERVER")
	fixture := os.Getenv("LANTERN_PROTECTED_QUERY_FIXTURE")
	driver := os.Getenv("LANTERN_PROTECTED_QUERY_DRIVER")
	if server == "" || fixture == "" || driver == "" {
		t.Skip("explicit protected-query preparation binaries required")
	}
	for _, binary := range []string{server, fixture, driver} {
		if !filepath.IsAbs(binary) {
			t.Fatal("absolute preparation binary paths required")
		}
	}
	var digest string
	for _, mode := range []string{"off", "oidc"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			_ = listener.Close()
			dir := filepath.Join(t.TempDir(), "owned")
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, fixture, "-protected-query", "-directory", dir, "-mode", mode, "-public-ports", strconv.Itoa(port), "-serve", server, "-ready-timeout", "60s")
			output, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			input, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			command.Stderr = &stderr
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			defer func() {
				_, _ = fmt.Fprintln(input, "shutdown")
				_ = input.Close()
				select {
				case err := <-done:
					if err != nil {
						t.Logf("fixture exit: %v; stderr: %s", err, stderr.String())
					}
				case <-time.After(10 * time.Second):
					cancel()
					<-done
					t.Error("owned fixture did not shut down")
				}
			}()
			var metadata json.RawMessage
			ready := make(chan error, 1)
			go func() { ready <- json.NewDecoder(output).Decode(&metadata) }()
			select {
			case err := <-ready:
				if err != nil {
					log, _ := os.ReadFile(filepath.Join(dir, "node-0-server.log"))
					failure, _ := os.ReadFile(filepath.Join(dir, "query-supervision-failure.log"))
					t.Fatalf("fixture readiness: %v; production log: %s; raw supervision failure: %s", err, log, failure)
				}
			case <-ctx.Done():
				t.Fatal("fixture readiness timeout")
			}
			metadataPath := filepath.Join(dir, "ready.json")
			if err := os.WriteFile(metadataPath, metadata, 0600); err != nil {
				t.Fatal(err)
			}
			for _, action := range []string{"seed", "preflight"} {
				probe := exec.CommandContext(ctx, driver, "-fixture", metadataPath, "-action", action)
				raw, err := probe.CombinedOutput()
				if err != nil {
					t.Fatalf("%s %s: %v: %s", mode, action, err, raw)
				}
				var result struct {
					Passed        bool              `json:"passed"`
					Corpus        string            `json:"corpus_sha256"`
					Transport     string            `json:"transport"`
					Qualification string            `json:"qualification"`
					Actor         string            `json:"reader_actor"`
					Measurements  map[string]string `json:"measurements"`
				}
				if json.Unmarshal(raw, &result) != nil || !result.Passed || result.Transport != "verified_tls_http2" || result.Qualification != "preparation_only" || mode == "oidc" && !strings.Contains(result.Actor, "jwt_role_bound") {
					t.Fatalf("wrong report contract: %s", raw)
				}
				for _, status := range result.Measurements {
					if status != "not_measured" {
						t.Fatal("preparation claimed uncollected performance evidence")
					}
				}
				if digest == "" {
					digest = result.Corpus
				} else if digest != result.Corpus {
					t.Fatal("OFF/ON logical corpus drift")
				}
				t.Logf("%s %s passed; corpus=%s; transport=%s; qualification=%s", mode, action, result.Corpus, result.Transport, result.Qualification)
			}
		})
	}
}
