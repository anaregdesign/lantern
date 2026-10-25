package security

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

func TestGenerateProvisioningFixedTopologiesAndSeparateKeys(t *testing.T) {
	for _, count := range []int{1, 3, 31} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			options := provisioningTestOptions(t, count, "linux")
			bundle, err := GenerateProvisioning(options)
			if err != nil || len(bundle.Nodes) != count {
				t.Fatal("fixed topology generation failed", err)
			}
			publicKeys := make(map[[32]byte]bool, 3*count+3)
			addKey := func(key []byte) {
				t.Helper()
				digest := sha256.Sum256(key)
				if publicKeys[digest] {
					t.Fatal("independent security responsibilities reused a key")
				}
				publicKeys[digest] = true
			}
			for _, raw := range [][]byte{bundle.Operator.CAKeyPEM, bundle.Operator.ControlKeyPEM, bundle.Operator.DataKeyPEM} {
				block, _ := pem.Decode(raw)
				key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
				if err != nil {
					t.Fatal("operator key invalid", err)
				}
				addKey(key.(ed25519.PrivateKey).Public().(ed25519.PublicKey))
			}
			for i, node := range bundle.Nodes {
				d := provisioningTestDocument(t, node.AuthorityJSON)
				if d.Participant.Member != uint32(i+1) || len(d.Genesis.Members) != count || len(d.Genesis.Origins) != count ||
					d.Participant.Incarnation != d.Genesis.Origins[i].Incarnation || node.CurrentProfile.Binding() != node.DataDomain.CurrentProfile {
					t.Fatal("generated node was not enrolled into the exact original topology")
				}
				description, _ := authorityTimeDescriptionForTarget(options.Target)
				c, profile, err := currentJSONConfigForTime(d, bundle.Operator.ControlManifest, description, false)
				if err != nil || profile != node.CurrentProfile || !c.Limits.valid(c.Participant) {
					t.Fatal("generated capacities or profile do not validate", err)
				}
				control, err := peerauth.VerifyControlManifest(bundle.Operator.ControlManifest, d.Membership.PublicKey, d.Membership.Profile.Lineage)
				if err != nil || control.ExpiresAt.Sub(control.IssuedAt) > peerauth.MaxMembershipLifetime || control.Profile.Digest() != profile.Membership {
					t.Fatal("initial control membership is not independently verifiable", err)
				}
				block, _ := pem.Decode(node.DataOperatorPublicKey)
				public, err := x509.ParsePKIXPublicKey(block.Bytes)
				if err != nil {
					t.Fatal("data operator public key is not PEM SPKI", err)
				}
				data, err := peerauth.VerifyManifest(node.DataManifest, public.(ed25519.PublicKey), node.DataDomain)
				if err != nil || len(data.Members) != count || data.Members[i] != node.DataSelf || !bytes.Equal(node.DataManifest, bundle.Operator.DataManifest) {
					t.Fatal("initial data membership is not independently verifiable", err)
				}
				pair, err := tls.X509KeyPair(node.CertificatePEM, node.TLSKeyPEM)
				if err != nil {
					t.Fatal("generated TLS material invalid", err)
				}
				addKey(pair.PrivateKey.(ed25519.PrivateKey).Public().(ed25519.PublicKey))
				addKey(ed25519.PrivateKey(d.Identity.VotingKey).Public().(ed25519.PublicKey))
				addKey(ed25519.PrivateKey(d.OriginKey).Public().(ed25519.PublicKey))
				for _, secret := range [][]byte{bundle.Operator.CAKeyPEM, bundle.Operator.ControlKeyPEM, bundle.Operator.DataKeyPEM} {
					if bytes.Contains(node.AuthorityJSON, bytes.TrimSpace(secret)) {
						t.Fatal("operator signer entered serving authority")
					}
				}
				if _, err := os.Stat(options.Nodes[i].StateDir); !os.IsNotExist(err) {
					t.Fatal("pure generator created a runtime directory", err)
				}
			}
		})
	}
}

func TestGenerateProvisioningExplicitPlatformAndTampering(t *testing.T) {
	var previous *ProvisioningBundle
	for _, target := range []string{"linux", "darwin"} {
		options := provisioningTestOptions(t, 1, target)
		bundle, err := GenerateProvisioning(options)
		if err != nil {
			t.Fatal(err)
		}
		d := provisioningTestDocument(t, bundle.Nodes[0].AuthorityJSON)
		description, _ := authorityTimeDescriptionForTarget(target)
		if err := ValidateProvisioningAuthorityJSON(bundle.Nodes[0].AuthorityJSON, bundle.Operator.ControlManifest, target, options.Image); err != nil {
			t.Fatal("pure original-image/key validation failed for explicit target", target, err)
		}
		profile, err := ProvisioningAuthorityProfile(bundle.Nodes[0].AuthorityJSON, bundle.Operator.ControlManifest, target)
		if err != nil || profile != bundle.Nodes[0].CurrentProfile {
			t.Fatal("pure profile accessor changed generated trust identity", err)
		}
		if err := ValidateProvisioningAuthorityPaths(bundle.Nodes[0].AuthorityJSON, options.Nodes[0].StateDir, options.Nodes[0].RenewableDir); err != nil {
			t.Fatal("generated paths do not match independently selected setup locations", err)
		}
		if err := ValidateProvisioningAuthorityPaths(bundle.Nodes[0].AuthorityJSON, options.Nodes[0].StateDir+"-other", options.Nodes[0].RenewableDir); err == nil {
			t.Fatal("generated paths accepted different expected custody root")
		}
		if d.Genesis.TimeProfile != sha256.Sum256([]byte(description)) || d.Genesis.Origins[0].Profile != authorityAdmissionProfileForTime(description) {
			t.Fatal("provisioning stamped the host's time semantics onto another platform")
		}
		if previous != nil && (previous.Nodes[0].CurrentProfile.Domain == bundle.Nodes[0].CurrentProfile.Domain ||
			bytes.Equal(previous.Nodes[0].AuthorityJSON, bundle.Nodes[0].AuthorityJSON)) {
			t.Fatal("independent provisioning reused its trust identity")
		}
		if target != runtime.GOOS {
			if _, _, err := currentJSONGenesis(d.Genesis); err == nil {
				t.Fatal("runtime accepted another target's time profile")
			}
		}
		original := d
		for _, tamper := range []func(*currentJSONProvisioningDocument){
			func(d *currentJSONProvisioningDocument) { d.Genesis.TimeProfile[0] ^= 1 },
			func(d *currentJSONProvisioningDocument) { d.Participant.BScope.ConfigDigest[0] ^= 1 },
			func(d *currentJSONProvisioningDocument) { d.Membership.Profile.ProtocolScope[0] ^= 1 },
			func(d *currentJSONProvisioningDocument) { d.Limits.QueueBytes = 1 },
		} {
			d = original
			tamper(&d)
			if _, _, err := currentJSONConfigForTime(d, bundle.Operator.ControlManifest, description, false); err == nil {
				t.Fatal("tampered generated trust passed validation")
			}
		}
		previous = bundle
	}
}

func TestGenerateProvisioningPureValidationRejectsSecretAndSeedTampering(t *testing.T) {
	options := provisioningTestOptions(t, 1, "linux")
	bundle, err := GenerateProvisioning(options)
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*currentJSONProvisioningDocument){
		"voting key":         func(d *currentJSONProvisioningDocument) { d.Identity.VotingKey[0] ^= 1 },
		"origin key":         func(d *currentJSONProvisioningDocument) { d.OriginKey[0] ^= 1 },
		"TLS private key":    func(d *currentJSONProvisioningDocument) { d.Identity.TLSKey = d.Identity.Certificate },
		"TLS certificate":    func(d *currentJSONProvisioningDocument) { d.Identity.Certificate = d.Identity.Roots },
		"relative state":     func(d *currentJSONProvisioningDocument) { d.Participant.PPath = "relative.wal" },
		"seed administrator": func(d *currentJSONProvisioningDocument) { d.Genesis.Image.Principals[0].Identity.Subject += "-changed" },
	} {
		t.Run(name, func(t *testing.T) {
			d := provisioningTestDocument(t, bundle.Nodes[0].AuthorityJSON)
			change(&d)
			raw, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateProvisioningAuthorityJSON(raw, bundle.Operator.ControlManifest, options.Target, options.Image); err == nil {
				t.Fatal("tampered seed/key/state was accepted before native initialization")
			}
		})
	}
}

func TestGenerateProvisioningRejectsInvalidIntent(t *testing.T) {
	for name, change := range map[string]func(*ProvisioningOptions){
		"implicit platform":        func(o *ProvisioningOptions) { o.Target = "" },
		"unsupported platform":     func(o *ProvisioningOptions) { o.Target = "windows" },
		"no voters":                func(o *ProvisioningOptions) { o.Nodes = nil },
		"too many voters":          func(o *ProvisioningOptions) { o.Nodes = make([]ProvisioningNode, 32) },
		"unknown image":            func(o *ProvisioningOptions) { o.Image = Image{} },
		"missing creation time":    func(o *ProvisioningOptions) { o.Now = time.Time{} },
		"expired operator window":  func(o *ProvisioningOptions) { o.End = o.Now },
		"missing cleanup capacity": func(o *ProvisioningOptions) { o.CleanupBy = o.End },
		"duplicate name":           func(o *ProvisioningOptions) { o.Nodes[1].Name = o.Nodes[0].Name },
		"endpoint credentials":     func(o *ProvisioningOptions) { o.Nodes[0].ControlOrigin = "https://secret@node:16380" },
		"endpoint path":            func(o *ProvisioningOptions) { o.Nodes[0].DataOrigin += "/path" },
		"duplicate endpoint":       func(o *ProvisioningOptions) { o.Nodes[1].DataOrigin = o.Nodes[0].DataOrigin },
		"relative custody":         func(o *ProvisioningOptions) { o.Nodes[0].StateDir = "state" },
		"aliased custody":          func(o *ProvisioningOptions) { o.Nodes[0].StateDir += "/../state" },
		"duplicate custody":        func(o *ProvisioningOptions) { o.Nodes[1].StateDir = o.Nodes[0].StateDir },
		"mixed mutable families":   func(o *ProvisioningOptions) { o.Nodes[0].RenewableDir = o.Nodes[0].StateDir },
		"listen port mismatch":     func(o *ProvisioningOptions) { o.Nodes[0].ControlListen = "0.0.0.0:16381" },
	} {
		t.Run(name, func(t *testing.T) {
			options := provisioningTestOptions(t, 3, "linux")
			change(&options)
			bundle, err := GenerateProvisioning(options)
			if err == nil || bundle != nil || strings.Contains(err.Error(), "secret@") {
				t.Fatal("invalid setup intent generated trust or leaked input", err)
			}
		})
	}
}

func TestGenerateProvisioningRuntimeLoaderOwnsSameValidatedProfile(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("generated native security supports explicit Linux and Darwin targets")
	}
	options := provisioningTestOptions(t, 1, runtime.GOOS)
	bundle, err := GenerateProvisioning(options)
	if err != nil {
		t.Fatal(err)
	}
	d := provisioningTestDocument(t, bundle.Nodes[0].AuthorityJSON)
	for _, dir := range []string{options.Nodes[0].StateDir, options.Nodes[0].RenewableDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(d.Membership.ManifestFile, bundle.Operator.ControlManifest, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCurrentProvisioningJSON(bundle.Nodes[0].AuthorityJSON)
	if err != nil || loaded.Profile() != bundle.Nodes[0].CurrentProfile {
		t.Fatal("generated profile failed runtime loader", err)
	}
	renewAt := options.Now.Add(7 * 24 * time.Hour)
	renewedRaw, _, err := RenewProvisioningIdentity(bundle.Operator.RootsPEM, bundle.Operator.CAKeyPEM, bundle.Nodes[0].AuthorityJSON,
		renewAt, renewAt.Add(2*time.Hour), options.Target)
	if err != nil {
		t.Fatal("fixed workload certificate maintenance failed", err)
	}
	renewed, err := LoadCurrentProvisioningJSON(renewedRaw)
	if err != nil || renewed.binding != loaded.binding || renewed.Profile() != loaded.Profile() {
		t.Fatal("same-key certificate maintenance changed native custody identity", err)
	}
	// The input is one immutable initial image; loading it never creates mutable
	// state, and changing its admitted capacity cannot preserve its identity.
	d.Participant.PendingCount++
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := LoadCurrentProvisioningJSON(raw)
	if err != nil || changed.binding == loaded.binding {
		t.Fatal("mutable capacity change was not bound into custody", err)
	}
}
