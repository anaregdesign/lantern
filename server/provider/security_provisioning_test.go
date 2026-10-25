package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/membershippublisher"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

func TestProvisionSecurityCreateOnlyCompleteRenewableBundle(t *testing.T) {
	options := provisioningTestOptions(t)
	if err := ProvisionSecurity(t.Context(), options); err != nil {
		t.Fatal("provision", err)
	}
	node := filepath.Join(options.OutputDirectory, "nodes", "node-1")
	path := filepath.Join(node, "security.json")
	config, err := loadSecurityConfigJSON(runtimeSecurityReference(path))
	if err != nil {
		t.Fatal("load provisioned reference", err)
	}
	if config.setup != nil || config.ProvisionedFile != path || config.ProvisionedPeer == nil || config.InitialImage == nil || config.StoreMode != "resume" ||
		config.ProvisionedSnapshotSpool == nil || *config.ProvisionedSnapshotSpool != provisionedSnapshotSpool(config.StateDirectory) ||
		config.ProvisionedPeer.Identity.CurrentProfile == "" || config.ProvisionedPeer.Identity.SecurityGeneration == [16]byte{} {
		t.Fatal("incomplete runtime composition")
	}
	if _, err := os.Stat(config.StateDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("provisioning created native state", err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"control-signing-key.pem", "data-signing-key.pem", "ca-signing-key.pem"} {
		key, err := os.ReadFile(filepath.Join(options.OperatorDirectory, secret))
		if err != nil || len(key) == 0 {
			t.Fatal("missing external signer", err)
		}
		if bytes.Contains(original, key) {
			t.Fatal("operator signer entered serving document")
		}
	}
	if err := filepath.WalkDir(options.OutputDirectory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Fatalf("public provisioning file permissions %s", entry.Name())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := sha256.Sum256(original)
	if err := ProvisionSecurity(t.Context(), options); err == nil {
		t.Fatal("existing output overwritten")
	}
	after, _ := os.ReadFile(path)
	if sha256.Sum256(after) != before {
		t.Fatal("repeated setup changed trust")
	}
	// The generated finite publisher, not a test fixture, can initialize and
	// renew both manifests while preserving its exact durable journal.
	publisherPath := filepath.Join(options.OperatorDirectory, "publisher.json")
	statePath := filepath.Join(options.OperatorDirectory, "state.json")
	publisher, err := membershippublisher.Open(publisherPath, statePath, "init")
	if err != nil {
		t.Fatal("generated publisher cannot initialize", err)
	}
	pubRaw, _ := os.ReadFile(publisherPath)
	var pc membershippublisher.Config
	if json.Unmarshal(pubRaw, &pc) != nil {
		t.Fatal("invalid publisher")
	}
	var control struct {
		Payload peerauth.ControlManifest `json:"payload"`
	}
	manifestPath := filepath.Join(node, "membership", "control.json")
	oldRaw, _ := os.ReadFile(manifestPath)
	if json.Unmarshal(oldRaw, &control) != nil {
		t.Fatal("invalid initial manifest")
	}
	initial := control.Payload
	if err := publisher.Step(initial.IssuedAt.Add(time.Minute)); err != nil {
		t.Fatal("generated renewal", err)
	}
	newRaw, _ := os.ReadFile(manifestPath)
	if json.Unmarshal(newRaw, &control) != nil || control.Payload.Version != initial.Version+1 || !control.Payload.ExpiresAt.After(initial.ExpiresAt) || control.Payload.Profile.Digest() != initial.Profile.Digest() {
		t.Fatal("renewal changed fixed trust or failed to extend lifetime")
	}
	if err := publisher.Close(); err != nil {
		t.Fatal(err)
	}
	publisher, err = membershippublisher.Open(publisherPath, statePath, "resume")
	if err != nil {
		t.Fatal("publisher exact resume", err)
	}
	if err := publisher.Step(pc.End); !errors.Is(err, membershippublisher.ErrBudget) {
		t.Fatal("publisher failed open after declared end", err)
	}
	if err := publisher.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := loadProvisionedSecurity(path); err != nil {
		t.Fatal("renewal invalidated serving completion", err)
	}
}

func TestProvisionSecurityInterruptedCreationCannotLoadOrInitialize(t *testing.T) {
	for _, failedName := range []string{"control-signing-key.pem", "ca-signing-key.pem", "publisher.json", "COMPLETE"} {
		t.Run(failedName, func(t *testing.T) {
			options := provisioningTestOptions(t)
			injected := errors.New("injected private write failure")
			err := provisionSecurityWithWriter(t.Context(), options, func(path string, raw []byte) error {
				if filepath.Base(path) == failedName {
					return injected
				}
				return writePrivateNewFile(path, raw)
			})
			if err == nil {
				t.Fatal("interruption not surfaced")
			}
			path := filepath.Join(options.OutputDirectory, "nodes", "node-1", "security.json")
			if _, err := loadProvisionedSecurity(path); err == nil {
				t.Fatal("partial output accepted before completion")
			}
			if err := ProvisionSecurity(t.Context(), options); err == nil {
				t.Fatal("partial output silently regenerated")
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(options.OutputDirectory), "state", "node-1")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed provisioning initialized state", err)
			}
		})
	}
}

func TestProvisionSecurityInputBoundaries(t *testing.T) {
	for name, edit := range map[string]func(*securitySetupJSON){
		"unqualified API admin": func(s *securitySetupJSON) {
			s.Issuers[0].ClientID = ""
			s.Issuers[0].ClientSecret = ""
			s.Issuers[0].APIAudience = "api"
			s.Issuers[0].ID = ""
		},
		"qualified API admin without purpose flow": func(s *securitySetupJSON) {
			s.Issuers[0].ClientID = ""
			s.Issuers[0].ClientSecret = ""
			s.Issuers[0].APIAudience = "api"
			s.Issuers[0].ID = ""
			s.Issuers[0].HumanSubjectNamespaceQualified = true
		},
		"missing subject":            func(s *securitySetupJSON) { s.InitialAdmin.Subject = "" },
		"target inferred":            func(s *securitySetupJSON) { s.TargetPlatform = "" },
		"implicit TLS boundary":      func(s *securitySetupJSON) { s.TLSTermination = "" },
		"server without certificate": func(s *securitySetupJSON) { s.TLSTermination = "server" },
		"duplicate nodes":            func(s *securitySetupJSON) { s.Nodes = append(s.Nodes, s.Nodes[0]) },
		"invalid private endpoint":   func(s *securitySetupJSON) { s.Nodes[0].DataOrigin += "/path" },
	} {
		t.Run(name, func(t *testing.T) {
			options := provisioningTestOptions(t)
			var setup securitySetupJSON
			json.Unmarshal([]byte(os.Getenv("LANTERN_SECURITY_CONFIG_JSON")), &setup)
			edit(&setup)
			raw, _ := json.Marshal(setup)
			t.Setenv("LANTERN_SECURITY_CONFIG_JSON", string(raw))
			err := ProvisionSecurity(t.Context(), options)
			if err == nil {
				t.Fatal("invalid intent provisioned")
			}
			if strings.Contains(err.Error(), "private-example") {
				t.Fatal("secret disclosed")
			}
			if _, err := os.Stat(options.OutputDirectory); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid intent wrote files", err)
			}
		})
	}
	options := provisioningTestOptions(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if !errors.Is(ProvisionSecurity(ctx, options), context.Canceled) {
		t.Fatal("cancellation ignored")
	}
}

func TestProvisionSecurityTamperingAndMissingInputsFailClosed(t *testing.T) {
	for _, name := range []string{"completion", "seed", "authority", "path", "snapshot path", "snapshot quota", "missing manifest"} {
		t.Run(name, func(t *testing.T) {
			options := provisioningTestOptions(t)
			if err := ProvisionSecurity(t.Context(), options); err != nil {
				t.Fatal(err)
			}
			node := filepath.Join(options.OutputDirectory, "nodes", "node-1")
			path := filepath.Join(node, "security.json")
			raw, _ := os.ReadFile(path)
			var doc provisionedSecurityDocument
			json.Unmarshal(raw, &doc)
			switch name {
			case "completion":
				os.Remove(filepath.Join(node, "COMPLETE"))
			case "seed":
				doc.Setup.InitialAdmin.Subject = "different-admin"
				raw, _ = json.Marshal(doc)
				os.WriteFile(path, raw, 0600)
			case "authority":
				doc.Authority = []byte(`{}`)
				raw, _ = json.Marshal(doc)
				os.WriteFile(path, raw, 0600)
			case "path":
				doc.Peer.State = filepath.Join(t.TempDir(), "new-state")
				raw, _ = json.Marshal(doc)
				os.WriteFile(path, raw, 0600)
			case "snapshot path":
				doc.SnapshotSpool.Directory = filepath.Join(t.TempDir(), "other-spool")
				raw, _ = json.Marshal(doc)
				os.WriteFile(path, raw, 0600)
			case "snapshot quota":
				doc.SnapshotSpool.MaxDiskBytes = 0
				raw, _ = json.Marshal(doc)
				os.WriteFile(path, raw, 0600)
			case "missing manifest":
				os.Remove(filepath.Join(node, "membership", "control.json"))
			}
			if _, err := loadProvisionedSecurity(path); err == nil {
				t.Fatal("tampered/missing input accepted")
			}
		})
	}
}

func TestProvisionSecurityRejectsCompetingAndUnknownInputs(t *testing.T) {
	for _, name := range []string{"LANTERN_SECURITY_CONFIG_FILE", "LANTERN_SECURITY_TYPO", "lantern_security_config_json", "LANTERN_OIDC_BROWSER_ORIGIN", "LANTERN_PEER_LISTEN_ADDR", "LANTERN_PEERS", "LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR"} {
		t.Run(name, func(t *testing.T) {
			options := provisioningTestOptions(t)
			t.Setenv(name, "")
			if err := ProvisionSecurity(t.Context(), options); err == nil {
				t.Fatal("competing/unknown setting ignored")
			}
			if _, err := os.Stat(options.OutputDirectory); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid environment wrote trust", err)
			}
		})
	}
}
