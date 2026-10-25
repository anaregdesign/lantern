package provider

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/anaregdesign/lantern/server/internal/membershippublisher"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

func TestProvisionedDataTrustRejectsSignedSubstitutionBeforeInitialization(t *testing.T) {
	for _, count := range []int{1, 3} {
		for _, change := range []string{"endpoint port", "remote identity", "remote key", "verification key", "CA digest"} {
			if count == 1 && (change == "remote identity" || change == "remote key") {
				continue
			}
			t.Run(fmt.Sprintf("nodes%d/%s", count, change), func(t *testing.T) {
				options := provisioningTestOptions(t)
				var setup securitySetupJSON
				if err := json.Unmarshal([]byte(os.Getenv("LANTERN_SECURITY_CONFIG_JSON")), &setup); err != nil {
					t.Fatal(err)
				}
				for i := 2; i <= count; i++ {
					setup.Nodes = append(setup.Nodes, securityNodeJSON{Name: fmt.Sprintf("node-%d", i), ControlOrigin: fmt.Sprintf("https://node-%d:6381", i), DataOrigin: fmt.Sprintf("https://node-%d:6382", i), ControlListen: ":6381", DataListen: ":6382"})
				}
				intent, _ := json.Marshal(setup)
				t.Setenv("LANTERN_SECURITY_CONFIG_JSON", string(intent))
				if err := ProvisionSecurity(t.Context(), options); err != nil {
					t.Fatal(err)
				}
				node := filepath.Join(options.OutputDirectory, "nodes", "node-1")
				path := filepath.Join(node, "security.json")
				original, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				config, err := loadProvisionedSecurity(path)
				if err != nil {
					t.Fatal("intact generation rejected", err)
				}
				expected := config.ProvisionedPeer.Identity
				if len(expected.FixedMembers) != count || expected.ExpectedDomain == (peerauth.Domain{}) || expected.ExpectedOperatorKey == [32]byte{} {
					t.Fatal("generation omitted immutable data trust")
				}
				raw, err := os.ReadFile(expected.ManifestFile)
				if err != nil {
					t.Fatal(err)
				}
				var envelope struct {
					Payload   peerauth.Manifest `json:"payload"`
					Signature []byte            `json:"signature"`
				}
				if err := json.Unmarshal(raw, &envelope); err != nil {
					t.Fatal(err)
				}
				keyPEM, err := os.ReadFile(filepath.Join(options.OperatorDirectory, "data-signing-key.pem"))
				if err != nil {
					t.Fatal(err)
				}
				block, _ := pem.Decode(keyPEM)
				if block == nil {
					t.Fatal("missing operator key")
				}
				parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
				if err != nil {
					t.Fatal(err)
				}
				signing, ok := parsed.(ed25519.PrivateKey)
				if !ok {
					t.Fatal("wrong operator key")
				}
				envelope.Payload.Version++
				switch change {
				case "endpoint port":
					// Same hostname and SPKI keep the existing workload certificate valid.
					envelope.Payload.Members[0].Origin = "https://node-1:6383"
				case "remote identity":
					envelope.Payload.Members[1].ID[0] ^= 0x80
				case "remote key":
					envelope.Payload.Members[1].SPKI[0] ^= 0x80
				case "verification key":
					var public ed25519.PublicKey
					public, signing, err = ed25519.GenerateKey(rand.Reader)
					if err != nil {
						t.Fatal(err)
					}
					der, err := x509.MarshalPKIXPublicKey(public)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(expected.OperatorKeyFile, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0600); err != nil {
						t.Fatal(err)
					}
				case "CA digest":
					// Equivalent PEM roots still change the originally pinned byte digest.
					roots, err := os.ReadFile(expected.CAFile)
					if err != nil {
						t.Fatal(err)
					}
					roots = append(roots, '\n')
					if err := os.WriteFile(expected.CAFile, roots, 0600); err != nil {
						t.Fatal(err)
					}
					envelope.Payload.Domain.TrustDigest = sha256.Sum256(roots)
				}
				substituted, err := peerauth.SignManifest(envelope.Payload, signing)
				if err != nil {
					t.Fatal("attack fixture is not a valid signed manifest", err)
				}
				verified, err := peerauth.VerifyManifest(substituted, signing.Public().(ed25519.PublicKey), envelope.Payload.Domain)
				if err != nil || len(verified.Members) != count {
					t.Fatal("fixture did not preserve a valid same-count signature", err)
				}
				if err := os.WriteFile(expected.ManifestFile, substituted, 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := loadSecurityConfigJSON(runtimeSecurityReference(path)); err == nil {
					t.Fatal("signed replacement became original trust before first init")
				}
				if _, err := os.Stat(config.StateDirectory); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("substitution created control/data custody", err)
				}
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(after, original) {
					t.Fatal("refusal rewrote signed original provisioning", err)
				}
				clear(signing)
				clear(keyPEM)
				clear(block.Bytes)
			})
		}
	}
}

func TestProvisionedDataTrustAllowsOnlySameOriginalTrustRenewal(t *testing.T) {
	options := provisioningTestOptions(t)
	if err := ProvisionSecurity(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(options.OutputDirectory, "nodes", "node-1", "security.json")
	config, err := loadProvisionedSecurity(path)
	if err != nil {
		t.Fatal(err)
	}
	before := config.ProvisionedPeer.Identity
	publisher, err := membershippublisher.Open(filepath.Join(options.OperatorDirectory, "publisher.json"), filepath.Join(options.OperatorDirectory, "state.json"), "init")
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	// Publisher advances only signed revision and interval, never the pinned set.
	raw, err := os.ReadFile(before.ManifestFile)
	if err != nil {
		t.Fatal(err)
	}
	var signed struct {
		Payload peerauth.Manifest `json:"payload"`
	}
	if err := json.Unmarshal(raw, &signed); err != nil {
		t.Fatal(err)
	}
	if err := publisher.Step(signed.Payload.IssuedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	after, err := loadProvisionedSecurity(path)
	if err != nil {
		t.Fatal("valid signed renewal rejected", err)
	}
	identity := after.ProvisionedPeer.Identity
	if identity.ExpectedDomain != before.ExpectedDomain || identity.ExpectedOperatorKey != before.ExpectedOperatorKey || !slices.Equal(identity.FixedMembers, before.FixedMembers) {
		t.Fatal("renewal changed original trust")
	}
}
