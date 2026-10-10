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
	testProtectedQueryPreparationWire(t, "legacy-v1")
}

// Native current readiness/custody is explicit and never replaced by fake time.
func TestProtectedQueryPreparation_CurrentProductionWire(t *testing.T) {
	if os.Getenv("LANTERN_PROTECTED_QUERY_CURRENT") != "1" {
		t.Skip("explicit current native host slot required")
	}
	testProtectedQueryPreparationWire(t, "current-v2")
}

func testProtectedQueryPreparationWire(t *testing.T, profile string) {
	server := os.Getenv("LANTERN_PROTECTED_QUERY_SERVER")
	fixture := os.Getenv("LANTERN_PROTECTED_QUERY_FIXTURE")
	driver := os.Getenv("LANTERN_PROTECTED_QUERY_DRIVER")
	if server == "" || fixture == "" || driver == "" {
		if profile == "current-v2" {
			t.Fatal("selected current wire gate requires all preparation binaries")
		}
		t.Skip("explicit protected-query preparation binaries required")
	}
	for _, binary := range []string{server, fixture, driver} {
		if !filepath.IsAbs(binary) {
			t.Fatal("absolute preparation binary paths required")
		}
	}
	exporter := os.Getenv("LANTERN_PROTECTED_QUERY_EXPORTER")
	if profile == "current-v2" && !filepath.IsAbs(exporter) {
		t.Fatal("absolute same-source original-input exporter required")
	}
	evidenceDir := ""
	if profile == "current-v2" {
		evidenceDir = os.Getenv("LANTERN_PROTECTED_QUERY_EVIDENCE_DIR")
		if !filepath.IsAbs(evidenceDir) {
			t.Fatal("new absolute private current evidence directory required")
		}
		if err := os.Mkdir(evidenceDir, 0700); err != nil {
			t.Fatal("current evidence directory must be new", err)
		}
	}
	var digest string
	for _, mode := range []string{"off", "oidc"} {
		t.Run(mode, func(t *testing.T) {
			count := 1
			readyTimeout := 60 * time.Second
			stopTimeout := 10 * time.Second
			totalTimeout := 90 * time.Second
			if profile == "current-v2" {
				count = 3
				readyTimeout = 180 * time.Second
				stopTimeout = 95 * time.Second
				totalTimeout = 5 * time.Minute
			}
			var ports []string
			var reservations []net.Listener
			for i := 0; i < count; i++ {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				reservations = append(reservations, listener)
				ports = append(ports, strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
			}
			for _, listener := range reservations {
				_ = listener.Close()
			}
			dir := ""
			if profile == "current-v2" {
				dir = filepath.Join(evidenceDir, mode)
			} else {
				dir = filepath.Join(t.TempDir(), "owned")
			}
			ctx, cancel := context.WithTimeout(t.Context(), totalTimeout)
			defer cancel()
			args := []string{"-protected-query", "-query-security-profile", profile, "-directory", dir, "-mode", mode, "-public-ports", strings.Join(ports, ","), "-serve", server, "-ready-timeout", readyTimeout.String()}
			if profile == "current-v2" {
				args = append(args, "-current-fixture-exporter", exporter)
			}
			command := exec.CommandContext(ctx, fixture, args...)

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
			var fixtureID string
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			defer func() {
				_, _ = fmt.Fprintln(input, "shutdown")
				_ = input.Close()
				select {
				case err := <-done:
					if err != nil {
						t.Errorf("fixture exit: %v; stderr: %s", err, stderr.String())
					}
				case <-time.After(stopTimeout):
					cancel()
					<-done
					t.Error("owned fixture did not shut down")
				}
				if profile == "current-v2" {
					var receipt struct {
						Passed    bool   `json:"passed"`
						Profile   string `json:"security_profile"`
						FixtureID string `json:"fixture_id"`
						Nodes     []struct {
							Passed bool   `json:"passed"`
							Exit   int    `json:"exit_code"`
							Cycle  uint64 `json:"cycle"`
							Floors string `json:"floors_sha256"`
						} `json:"nodes"`
					}
					raw, err := os.ReadFile(filepath.Join(dir, "query-shutdown.json"))
					if err != nil || json.Unmarshal(raw, &receipt) != nil || !receipt.Passed || receipt.Profile != "current-v2" || receipt.FixtureID != fixtureID || len(fixtureID) != 32 || len(receipt.Nodes) != 3 {
						t.Errorf("missing successful current teardown receipt: %v", err)
					} else {
						for _, node := range receipt.Nodes {
							if !node.Passed || node.Exit != 0 || mode == "oidc" && (node.Cycle != 1 || len(node.Floors) != 64) {
								t.Error("current native child did not finish CLEAN")
							}
						}
					}
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
			var selected struct {
				Query struct {
					FixtureID string `json:"fixture_id"`
				} `json:"protected_query"`
			}
			if err := json.Unmarshal(metadata, &selected); err != nil {
				t.Fatal(err)
			}
			fixtureID = selected.Query.FixtureID
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
				if err := os.WriteFile(filepath.Join(dir, action+"-report.json"), raw, 0600); err != nil {
					t.Fatal(err)
				}
				var result struct {
					Passed        bool              `json:"passed"`
					Corpus        string            `json:"corpus_sha256"`
					Transport     string            `json:"transport"`
					Qualification string            `json:"qualification"`
					Actor         string            `json:"reader_actor"`
					Profile       string            `json:"security_profile"`
					FixtureID     string            `json:"fixture_id"`
					Processes     int               `json:"server_processes"`
					Expected      []string          `json:"expected_search_keys"`
					Axis          string            `json:"comparison_axis"`
					Measurements  map[string]string `json:"measurements"`
				}
				if json.Unmarshal(raw, &result) != nil || !result.Passed || result.Transport != "verified_tls_http2" || result.Qualification != "preparation_only" || mode == "oidc" && !strings.Contains(result.Actor, "jwt_role_bound") || result.Profile != profile || result.Processes != count || profile == "current-v2" && result.FixtureID != fixtureID || result.Axis != "end_to_end_mode_specific_authorized_results" || len(result.Expected) != map[string]int{"off": 3, "oidc": 2}[mode] {
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
