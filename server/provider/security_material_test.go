package provider

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestSecurityMaterialBindsSeparateJournalAndCustodyLocations(t *testing.T) {
	for _, directRoot := range []bool{false, true} {
		t.Run(map[bool]string{false: "node directory", true: "persistence root"}[directRoot], func(t *testing.T) {
			options := provisioningTestOptions(t)
			if err := ProvisionSecurity(t.Context(), options); err != nil {
				t.Fatal(err)
			}
			currentJSON := os.Getenv("LANTERN_SECURITY_CONFIG_JSON")
			config, err := loadSecurityConfigJSON(currentJSON)
			if err != nil {
				t.Fatal(err)
			}
			document, _, err := readSecurityMaterial(config.MaterialFile)
			if err != nil {
				t.Fatal(err)
			}
			originalState := document.StatePaths.Directory
			if directRoot {
				document.StatePaths.Directory = document.StatePaths.PersistenceRoot
			}
			document.StatePaths.CustodyDirectory = filepath.Join(filepath.Dir(config.MaterialRoot), "custody")
			var authority map[string]json.RawMessage
			if err := json.Unmarshal(document.Authority, &authority); err != nil {
				t.Fatal(err)
			}
			authority["floors_file"], _ = json.Marshal(filepath.Join(document.StatePaths.CustodyDirectory, "retained-floors.json"))
			if directRoot {
				for _, name := range []string{"participant", "membership"} {
					authority[name] = bytes.ReplaceAll(authority[name], []byte(originalState+"/"), []byte(document.StatePaths.Directory+"/"))
				}
				document.Peer.State = filepath.Join(document.StatePaths.Directory, "data-membership")
				document.SnapshotSpool = provisionedSnapshotSpool(document.StatePaths.Directory)
			}
			document.Authority, _ = json.Marshal(authority)
			writeSecurityMaterialTestDocument(t, config.MaterialFile, document, options.OperatorDirectory)
			loaded, err := loadSecurityConfigJSON(currentJSON)
			if err != nil || loaded.StateDirectory != document.StatePaths.Directory || loaded.CustodyDirectory != document.StatePaths.CustodyDirectory || loaded.PersistenceRoot != document.StatePaths.PersistenceRoot {
				t.Fatal("genuine split custody material was rejected or rebased", err)
			}
			for _, path := range []string{originalState, loaded.CustodyDirectory, loaded.ProvisionedSnapshotSpool.Directory} {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("material validation created native storage", err)
				}
			}
			for name, change := range map[string]func(*securityMaterialDocument){
				"unsigned descriptor":    func(d *securityMaterialDocument) { d.StatePaths.CustodyDirectory += "-substituted" },
				"authority disagreement": func(d *securityMaterialDocument) { d.StatePaths.CustodyDirectory += "-other" },
				"material overlap":       func(d *securityMaterialDocument) { d.StatePaths.CustodyDirectory = config.MaterialRoot },
				"noncanonical custody":   func(d *securityMaterialDocument) { d.StatePaths.CustodyDirectory += "/../alias" },
				"state outside root":     func(d *securityMaterialDocument) { d.StatePaths.Directory = d.StatePaths.CustodyDirectory },
			} {
				t.Run(name, func(t *testing.T) {
					bad := document
					change(&bad)
					if name == "unsigned descriptor" {
						raw, _ := json.Marshal(bad)
						if err := os.WriteFile(config.MaterialFile, raw, 0600); err != nil {
							t.Fatal(err)
						}
					} else {
						writeSecurityMaterialTestDocument(t, config.MaterialFile, bad, options.OperatorDirectory)
					}
					if _, err := loadSecurityConfigJSON(currentJSON); err == nil {
						t.Fatal("mismatched custody descriptor was accepted")
					}
					writeSecurityMaterialTestDocument(t, config.MaterialFile, document, options.OperatorDirectory)
				})
			}
			var input securityConfigJSON
			if err := json.Unmarshal([]byte(currentJSON), &input); err != nil {
				t.Fatal(err)
			}
			input.PersistenceRoot += "-different"
			changed, _ := json.Marshal(input)
			if _, err := loadSecurityConfigJSON(string(changed)); err == nil {
				t.Fatal("current persistence root disagreed with signed material")
			}
		})
	}
}

func writeSecurityMaterialTestDocument(t *testing.T, path string, document securityMaterialDocument, operator string) {
	t.Helper()
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(filepath.Join(operator, "control-signing-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(key)
	publisher, err := os.ReadFile(filepath.Join(operator, "publisher.json"))
	if err != nil {
		t.Fatal(err)
	}
	marker, err := SecurityMaterialCompletion(raw, sha256.Sum256(publisher), key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "COMPLETE"), marker, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSecurityMaterialRejectsNativePeerAndSpoolFamilyAliases(t *testing.T) {
	options := provisioningTestOptions(t)
	if err := ProvisionSecurity(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	currentJSON := os.Getenv("LANTERN_SECURITY_CONFIG_JSON")
	config, err := loadSecurityConfigJSON(currentJSON)
	if err != nil {
		t.Fatal(err)
	}
	original, _, err := readSecurityMaterial(config.MaterialFile)
	if err != nil {
		t.Fatal(err)
	}
	for name, replacement := range map[string]struct{ section, field, path string }{
		"peer checkpoint":         {"participant", "PPath", original.Peer.State},
		"peer lease":              {"participant", "BPath", original.Peer.State + ".lease"},
		"peer replacement":        {"membership", "path", original.Peer.State + ".next-reserved"},
		"spool directory":         {"membership", "path", original.SnapshotSpool.Directory},
		"floors native collision": {"", "floors_file", filepath.Join(original.StatePaths.Directory, "control-protocol.wal.lease")},
	} {
		t.Run(name, func(t *testing.T) {
			document := original
			var authority map[string]json.RawMessage
			if err := json.Unmarshal(document.Authority, &authority); err != nil {
				t.Fatal(err)
			}
			path, _ := json.Marshal(replacement.path)
			if replacement.section == "" {
				authority[replacement.field] = path
			} else {
				var section map[string]json.RawMessage
				if err := json.Unmarshal(authority[replacement.section], &section); err != nil {
					t.Fatal(err)
				}
				section[replacement.field] = path
				authority[replacement.section], _ = json.Marshal(section)
			}
			document.Authority, _ = json.Marshal(authority)
			writeSecurityMaterialTestDocument(t, config.MaterialFile, document, options.OperatorDirectory)
			if _, err := loadSecurityConfigJSON(currentJSON); err == nil {
				t.Fatal("signed colliding storage families accepted")
			}
			if _, err := os.Stat(original.StatePaths.Directory); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("collision refusal created native state", err)
			}
		})
	}
}

func TestHistoricalSeedIdentityPreservesManagedPolicy(t *testing.T) {
	admin := securityInitialAdminJSON{Issuer: "https://managed.example", Subject: "operator-admin"}
	image := security.Image{Version: security.ImageVersion, BootstrapRevision: 1,
		Issuers:    []security.Issuer{{URL: admin.Issuer, Enabled: true, ConfigRevision: 1, ClientID: "managed-client", RedirectURI: "https://admin.example/callback", Algorithms: []string{"EdDSA"}}},
		Roles:      []security.Role{{ID: "custom_admin", Name: "Externally managed administrator", Rules: []security.PermissionRule{{ID: "manage", Effect: security.Allow, Action: security.SecurityManage, Resource: security.GlobalResource}}}},
		Principals: []security.Principal{{Identity: security.Identity{Kind: security.OIDCPrincipal, Issuer: admin.Issuer, Subject: admin.Subject}, State: security.Active, HumanIssuerConfigRevision: 1, Assignments: []security.RoleAssignment{{RoleID: "custom_admin"}}}},
	}
	if !historicalSeedIdentity(image, admin) {
		t.Fatal("historical assertion rejected valid managed trust or custom administrator Role")
	}
	if historicalSeedIdentity(image, securityInitialAdminJSON{Issuer: admin.Issuer, Subject: "different"}) {
		t.Fatal("historical assertion accepted a different identity")
	}
	if image.Issuers[0].EnvOwned || image.Principals[0].Assignments[0].EnvOwned || image.Roles[0].ID != "custom_admin" {
		t.Fatal("historical assertion rewrote managed policy")
	}
}

func TestSecurityConfigurationEveryLoadConsumesSuppliedIssuersAndSecrets(t *testing.T) {
	options := provisioningTestOptions(t)
	if err := ProvisionSecurity(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	original, err := loadSecurityConfigJSON(os.Getenv("LANTERN_SECURITY_CONFIG_JSON"))
	if err != nil {
		t.Fatal(err)
	}
	material, err := os.ReadFile(original.MaterialFile)
	if err != nil {
		t.Fatal(err)
	}
	var input securityConfigJSON
	if err := json.Unmarshal([]byte(os.Getenv("LANTERN_SECURITY_CONFIG_JSON")), &input); err != nil {
		t.Fatal(err)
	}
	input.Issuers[0].ClientSecret = "second-current-secret"
	input.Issuers = append(input.Issuers, securityIssuerJSON{Issuer: "https://second.example", APIAudience: "second-api", Algorithms: []string{"EdDSA"}})
	input.TrustedProxyIPs = []string{"127.0.0.2"}
	input.Nodes[0].ControlListen = "127.0.0.1:6381"
	input.Nodes[0].DataListen = "127.0.0.1:0"
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LANTERN_SECURITY_CONFIG_JSON", string(raw))
	current, err := loadSecurityConfig()
	if err != nil {
		t.Fatal("changed current configuration rejected", err)
	}
	if len(current.Issuers) != 2 || current.Issuers[1].URL != "https://second.example" || current.SecretBindings["google_web"].Value != "second-current-secret" || len(current.TrustedProxyIPs) != 1 || current.TrustedProxyIPs[0].String() != "127.0.0.2" || current.ProvisionedPeer.ListenAddress != "127.0.0.1:0" {
		t.Fatal("reload substituted historical provider configuration for supplied values")
	}
	if original.SecretBindings["google_web"].Value != "private-example" || len(original.Issuers) != 1 || current.HistoricalImage == nil || len(current.HistoricalImage.Issuers) != 1 {
		t.Fatal("reload mutated a prior invocation or historical trust anchor")
	}
	input.Issuers = []securityIssuerJSON{}
	raw, err = json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LANTERN_SECURITY_CONFIG_JSON", string(raw))
	empty, err := loadSecurityConfig()
	if err != nil || len(empty.Issuers) != 0 || empty.HistoricalImage == nil || len(empty.HistoricalImage.Issuers) != 1 {
		t.Fatal("explicit empty Issuers were replaced by saved issuer configuration", err)
	}
	for _, bad := range []string{"", `{"provisioned_file":"` + original.MaterialFile + `"}`} {
		t.Setenv("LANTERN_SECURITY_CONFIG_JSON", bad)
		if _, err := loadSecurityConfig(); err == nil {
			t.Fatal("missing or retired configuration selected stored provider input")
		}
	}
	if err := os.Unsetenv("LANTERN_SECURITY_CONFIG_JSON"); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSecurityConfig(); err == nil {
		t.Fatal("missing every-start configuration was ignored")
	}
	after, err := os.ReadFile(original.MaterialFile)
	if err != nil || !bytes.Equal(material, after) {
		t.Fatal("configuration reload rewrote identity material", err)
	}
	if original.HistoricalImage.Principals[0].Identity != (security.Identity{Kind: security.OIDCPrincipal, Issuer: security.GoogleIssuer, Subject: "initial-admin"}) {
		t.Fatal("reload reset the historical initial identity")
	}
}
