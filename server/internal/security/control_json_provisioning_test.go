package security

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

func currentJSONProvisioningFixture(t *testing.T) (currentJSONProvisioningDocument, *CurrentProvisioning, *authorityOriginOwner) {
	t.Helper()
	p, origin := currentCustodyFixture(t)
	var document currentJSONProvisioningDocument
	if err := decodeCurrentJSONProvisioning(p.jsonSource, &document); err != nil {
		t.Fatal(err)
	}
	return document, p, origin
}

func currentJSONEncode(t *testing.T, d currentJSONProvisioningDocument) []byte {
	t.Helper()
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func currentJSONNewStatePaths(t *testing.T, d *currentJSONProvisioningDocument) {
	t.Helper()
	dir := t.TempDir()
	d.Participant.PPath, d.Participant.BPath = filepath.Join(dir, "p"), filepath.Join(dir, "b")
	d.Membership.Path, d.FloorsFile = filepath.Join(dir, "membership"), filepath.Join(dir, "floors.json")
}

func TestCurrentProvisioningValidatesCompleteOriginalImage(t *testing.T) {
	options := provisioningTestOptions(t, 1, "linux")
	bundle, err := GenerateProvisioning(options)
	if err != nil {
		t.Fatal(err)
	}
	d := provisioningTestDocument(t, bundle.Nodes[0].AuthorityJSON)
	description, _ := authorityTimeDescriptionForTarget(options.Target)
	c, _, err := currentJSONConfigForTime(d, bundle.Operator.ControlManifest, description, false)
	if err != nil {
		t.Fatal(err)
	}
	p := &CurrentProvisioning{config: c}
	if err := p.ValidateInitialImage(d.Genesis.Image); err != nil {
		t.Fatal("unchanged configured seed did not match immutable genesis", err)
	}
	if err := (*CurrentProvisioning)(nil).ValidateInitialImage(d.Genesis.Image); err == nil {
		t.Fatal("absent immutable genesis accepted a seed")
	}
	for _, change := range []func(*Image){
		func(image *Image) { image.BootstrapRevision++ },
		func(image *Image) { image.Principals[0].Identity.Subject += "-changed" },
		func(image *Image) { image.Issuers[0].APIAudience += "-changed" },
		func(image *Image) { image.Roles[0].Name += "-changed" },
	} {
		image := p.config.Participant.Trust.genesis.state.projection.Image()
		change(&image)
		if err := p.ValidateInitialImage(image); err == nil {
			t.Fatal("changed original seed was silently treated as mutable runtime state")
		}
	}
	if err := p.ValidateInitialImage(d.Genesis.Image); err != nil {
		t.Fatal("seed comparison mutated immutable genesis", err)
	}
}

func TestCurrentJSONProvisioningOwnsCanonicalInlineMaterial(t *testing.T) {
	d, old, _ := currentJSONProvisioningFixture(t)
	raw := currentJSONEncode(t, d)
	p, err := LoadCurrentProvisioningJSON(raw)
	if err != nil || p.Profile() != old.Profile() || p.binding != old.binding || !bytes.Equal(p.jsonSource, old.jsonSource) || p.config.Identity.material == nil {
		t.Fatal("inline source did not preserve canonical identity and owned material", err)
	}
	clear(raw)
	reloaded, err := p.reload()
	if err != nil || reloaded.binding != p.binding || reloaded.Profile() != p.Profile() {
		t.Fatal("owned inline material depended on caller bytes", err)
	}
	formatted, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := LoadCurrentProvisioningJSON(formatted)
	if err != nil || canonical.binding != p.binding || !bytes.Equal(canonical.jsonSource, p.jsonSource) {
		t.Fatal("equivalent formatting changed canonical inline history", err)
	}
	d.Identity.TLSKey += "\n" // Equivalent private PEM representation, same identity.
	equivalent, err := LoadCurrentProvisioningJSON(currentJSONEncode(t, d))
	if err != nil || equivalent.binding != p.binding {
		t.Fatal("secret bytes entered the public custody commitment", err)
	}
	manifest, err := os.ReadFile(d.Membership.ManifestFile)
	if err != nil {
		t.Fatal(err)
	}
	d.Membership.ManifestFile = filepath.Join(t.TempDir(), "renewable.signed")
	if err := os.WriteFile(d.Membership.ManifestFile, manifest, 0600); err != nil {
		t.Fatal(err)
	}
	relocated, err := LoadCurrentProvisioningJSON(currentJSONEncode(t, d))
	if err != nil || relocated.binding != p.binding {
		t.Fatal("configuration input path entered custody identity", err)
	}
	votingKey := bytes.Clone(p.config.Identity.material.votingKey)
	d.Identity.VotingKey[0] ^= 1
	d.OriginKey[0] ^= 1
	if !bytes.Equal(votingKey, p.config.Identity.material.votingKey) || !bytes.Equal(p.jsonSource, old.jsonSource) {
		t.Fatal("decoded caller material aliased owned identity or inline history")
	}
}

func TestCurrentJSONProvisioningWrongPrivateKeysCreateNoState(t *testing.T) {
	d, _, _ := currentJSONProvisioningFixture(t)
	for _, kind := range []string{"voting", "tls", "origin"} {
		t.Run(kind, func(t *testing.T) {
			copy := d
			currentJSONNewStatePaths(t, &copy)
			switch kind {
			case "voting":
				copy.Identity.VotingKey = bytes.Clone(d.Identity.VotingKey)
				copy.Identity.VotingKey[0] ^= 1
			case "tls":
				copy.Identity.TLSKey = "invalid private key"
			case "origin":
				copy.OriginKey = bytes.Clone(d.OriginKey)
				copy.OriginKey[0] ^= 1
			}
			p, err := LoadCurrentProvisioningJSON(currentJSONEncode(t, copy))
			if err != nil {
				t.Fatal("public source validation failed before the runtime key check", err)
			}
			if kind == "origin" {
				if _, err := loadAuthorityOriginKey(p.config, ""); err == nil {
					t.Fatal("wrong origin identity accepted")
				}
			} else if owner, err := createS3AOwner(p.config); err == nil {
				_ = owner.Close()
				t.Fatal("wrong private identity opened native state")
			}
			for _, path := range []string{copy.Participant.PPath, copy.Participant.BPath, copy.Membership.Path, copy.FloorsFile, copy.FloorsFile + ".state"} {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("invalid key created persistent state", path, err)
				}
			}
		})
	}
}

// Uses the existing private deterministic time fixture to check bootstrap and
// lifecycle, not to qualify a configured production time source or provider.
func TestCurrentJSONProvisioningInitCleanAndResumeWithoutQuorum(t *testing.T) {
	d, _, original := currentJSONProvisioningFixture(t)
	currentJSONNewStatePaths(t, &d)
	p, err := LoadCurrentProvisioningJSON(currentJSONEncode(t, d))
	if err != nil {
		t.Fatal(err)
	}
	if _, custody, _, err := openCurrentCustody(p, "resume"); err == nil {
		_ = custody.close()
		t.Fatal("ordinary start silently initialized absent state")
	}
	open := func(p *CurrentProvisioning, mode string) *CurrentAuthority {
		t.Helper()
		verified, custody, floors, err := openCurrentCustody(p, mode)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = custody.close() })
		c := verified.config
		clock, _ := fakeAuthorityTimeOwnerAt(t, original.network.now())
		if err := bindAuthorityNetworkTime(&c, clock); err != nil {
			t.Fatal(err)
		}
		c.hooks = &s3aHooks{beforeRenewal: func(ctx context.Context) { <-ctx.Done() }}
		if err := custody.running(); err != nil {
			t.Fatal(err)
		}
		network, err := openS3AOwner(c, mode == "fresh", floors)
		if err != nil {
			_ = custody.close()
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = network.Close() })
		key, err := loadAuthorityOriginKey(c, "")
		if err != nil {
			t.Fatal(err)
		}
		owner, err := attachAuthorityOrigin(network, key, "https://admin.example")
		if err != nil {
			t.Fatal(err)
		}
		result := &CurrentAuthority{origin: owner, custody: custody, manifest: p.manifest}
		t.Cleanup(func() { _ = result.Close() })
		if result.Ready(t.Context()) {
			t.Fatal("bootstrap manufactured quorum serving authority")
		}
		observed, err := network.Floors()
		if err != nil || !currentCustodyFloors(observed, p) {
			t.Fatal("bootstrap did not durably initialize all minimum floors", err)
		}
		return result
	}
	owner := open(p, "fresh")
	if err := owner.Shutdown(); err != nil || currentCustodyReadState(t, p).Phase != "CLEAN" {
		t.Fatal("init required serving authority or lost CLEAN", err)
	}
	before, err := os.ReadFile(p.floors + ".state")
	if err != nil {
		t.Fatal(err)
	}
	changed := d
	changed.Limits.Rounds++
	other, err := LoadCurrentProvisioningJSON(currentJSONEncode(t, changed))
	if err != nil || other.binding == p.binding {
		t.Fatal("changed original public configuration retained custody identity", err)
	}
	if _, custody, _, err := openCurrentCustody(other, "resume"); err == nil {
		_ = custody.close()
		t.Fatal("different configuration resumed existing state")
	}
	after, err := os.ReadFile(p.floors + ".state")
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("rejected resume modified retained CLEAN", err)
	}
	resumed := open(p, "resume")
	if err := resumed.Shutdown(); err != nil || currentCustodyReadState(t, p).Cycle != 2 {
		t.Fatal("unchanged original JSON did not complete orderly resume", err)
	}
}

func TestCurrentJSONProvisioningEquivalentInlineSourceRetainsCustody(t *testing.T) {
	d, source, origin := currentJSONProvisioningFixture(t)
	custody := currentCustodyOpenTest(t, source, "fresh")
	if err := custody.running(); err != nil {
		t.Fatal(err)
	}
	owner := &CurrentAuthority{origin: origin, custody: custody}
	if err := owner.Shutdown(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(source.floors + ".state")
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	equivalent, err := LoadCurrentProvisioningJSON(formatted)
	if err != nil || equivalent.Profile() != source.Profile() || equivalent.binding != source.binding || !bytes.Equal(equivalent.jsonSource, source.jsonSource) {
		t.Fatal("equivalent inline input changed retained custody identity", err)
	}
	if _, c, _, err := openCurrentCustody(equivalent, "resume"); err != nil {
		t.Fatal("equivalent canonical input could not resume original custody", err)
	} else if err := c.close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(source.floors + ".state")
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("read-only equivalent resume changed original CLEAN", err)
	}
	if _, c, _, err := openCurrentCustody(source, "resume"); err != nil {
		t.Fatal("equivalent input damaged ordinary original resume", err)
	} else if err := c.close(); err != nil {
		t.Fatal(err)
	}
}

func TestCurrentJSONProvisioningOriginalGenesis(t *testing.T) {
	d, old, _ := currentJSONProvisioningFixture(t)
	trust, state, err := currentJSONGenesis(d.Genesis)
	if err != nil || trust.scope != old.config.Participant.Trust.scope || state.configuration.Digest() != old.Profile().Configuration {
		t.Fatal("original public genesis changed with source transport", err)
	}
	for name, edit := range map[string]func(*currentGenesisDocument){
		"version":      func(g *currentGenesisDocument) { g.Version++ },
		"time":         func(g *currentGenesisDocument) { g.TimeProfile[0] ^= 1 },
		"retirement":   func(g *currentGenesisDocument) { g.Roots.Retirement[0] = 1 },
		"missing root": func(g *currentGenesisDocument) { g.Roots.Genesis = [32]byte{} },
	} {
		t.Run(name, func(t *testing.T) {
			copy := d.Genesis
			edit(&copy)
			if _, _, err := currentJSONGenesis(copy); err == nil {
				t.Fatal("incompatible original trust accepted")
			}
		})
	}
}

func TestCurrentJSONProvisioningOriginalEnrollment(t *testing.T) {
	d, old, _ := currentJSONProvisioningFixture(t)
	if err := currentJSONEnrollment(old.config, d.ListenAddress, true); err != nil {
		t.Fatal("original enrollment rejected", err)
	}
	for name, edit := range map[string]func(*s3aConfig){
		"unknown member":      func(c *s3aConfig) { c.Participant.Member = 99 },
		"wrong origin":        func(c *s3aConfig) { c.Participant.OwnedOrigin = 2 },
		"missing incarnation": func(c *s3aConfig) { c.Participant.Incarnation = [16]byte{} },
		"foreign self":        func(c *s3aConfig) { c.Membership.Self.ID[0] ^= 1 },
	} {
		t.Run(name, func(t *testing.T) {
			copy := old.config
			edit(&copy)
			if currentJSONEnrollment(copy, d.ListenAddress, true) == nil {
				t.Fatal("changed original enrollment accepted")
			}
		})
	}
	if currentJSONEnrollment(old.config, d.ListenAddress, false) == nil || currentJSONEnrollment(old.config, "127.0.0.1:0", true) == nil {
		t.Fatal("missing key or invalid listener accepted")
	}
}

func TestCurrentJSONProvisioningSignedConfig(t *testing.T) {
	d, old, _ := currentJSONProvisioningFixture(t)
	manifest, err := os.ReadFile(d.Membership.ManifestFile)
	if err != nil {
		t.Fatal(err)
	}
	c, profile, err := currentJSONConfig(d, manifest)
	if err != nil || profile != old.Profile() || c.Identity.material == nil {
		t.Fatal("source transport changed signed configuration", err)
	}
	for name, edit := range map[string]func(*currentJSONProvisioningDocument){
		"public key":      func(d *currentJSONProvisioningDocument) { d.Membership.PublicKey = []byte("invalid") },
		"scope":           func(d *currentJSONProvisioningDocument) { d.Membership.Profile.ProtocolScope[0] ^= 1 },
		"B scope":         func(d *currentJSONProvisioningDocument) { d.Participant.BScope.Cohort[0] ^= 1 },
		"resource limits": func(d *currentJSONProvisioningDocument) { d.Limits.Attempts = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			copy := d
			edit(&copy)
			if _, _, err := currentJSONConfig(copy, manifest); err == nil {
				t.Fatal("incompatible signed configuration accepted")
			}
		})
	}
	if _, _, err := currentJSONConfig(d, []byte("unsigned")); err == nil {
		t.Fatal("unsigned membership accepted")
	}
}

func TestCurrentJSONProvisioningStrictShape(t *testing.T) {
	type sample struct {
		Digest [2]byte `json:"digest"`
		Key    []byte  `json:"key"`
	}
	for name, raw := range map[string]string{
		"canonical":           `{"digest":[1,2],"key":"AQI="}`,
		"formatted reordered": "{\n\"key\": \"AQI=\", \"digest\": [1, 2]\n}",
	} {
		t.Run(name, func(t *testing.T) {
			var result sample
			if err := decodeCurrentJSONProvisioning([]byte(raw), &result); err != nil || result.Digest != [2]byte{1, 2} || len(result.Key) != 2 {
				t.Fatal("valid typed input rejected", err)
			}
		})
	}
	for name, raw := range map[string]string{
		"short array":   `{"digest":[1],"key":"AQI="}`,
		"long array":    `{"digest":[1,2,3],"key":"AQI="}`,
		"duplicate":     `{"digest":[1,2],"digest":[1,2],"key":"AQI="}`,
		"alias":         `{"Digest":[1,2],"key":"AQI="}`,
		"unknown":       `{"digest":[1,2],"key":"AQI=","ready":true}`,
		"numeric bytes": `{"digest":[1,2],"key":[1,2]}`,
		"trailing":      `{"digest":[1,2],"key":"AQI="}{}`,
	} {
		t.Run(name, func(t *testing.T) {
			var result sample
			if decodeCurrentJSONProvisioning([]byte(raw), &result) == nil {
				t.Fatal("ambiguous typed input accepted")
			}
		})
	}
	raw, err := json.Marshal(currentJSONProvisioningDocument{Version: currentJSONProvisioningVersion})
	if err != nil {
		t.Fatal(err)
	}
	if owner, err := LoadCurrentProvisioningJSON(raw); err == nil || owner != nil {
		t.Fatal("incomplete original provisioning created an owner")
	}
}

func TestCurrentProvisioningConfiguredIntentPurePreflight(t *testing.T) {
	for _, target := range []string{"linux", "darwin"} {
		t.Run(target, func(t *testing.T) {
			options := provisioningTestOptions(t, 3, target)
			bundle, err := GenerateProvisioning(options)
			if err != nil {
				t.Fatal(err)
			}
			desired, err := NewConfiguredTrust(options.Image.Issuers)
			if err != nil {
				t.Fatal(err)
			}
			for _, node := range bundle.Nodes {
				if err = ValidateProvisioningConfiguredTrust(node.AuthorityJSON, bundle.Operator.ControlManifest, target, desired); err != nil {
					t.Fatal("pure target-specific capability validation", err)
				}
				historical, err := ProvisioningInitialImage(node.AuthorityJSON)
				if err != nil {
					t.Fatal(err)
				}
				snapshot, _ := CompileImage(historical, DefaultPolicyLimits())
				original, _ := CompileImage(options.Image, DefaultPolicyLimits())
				if !bytes.Equal(snapshot.image, original.image) {
					t.Fatal("historical seed changed during configured preflight")
				}
				historical.Issuers[0].ClientID = "caller mutation"
				untouched, err := ProvisioningInitialImage(node.AuthorityJSON)
				if err != nil || untouched.Issuers[0].ClientID != options.Image.Issuers[0].ClientID {
					t.Fatal("historical seed data aliased caller", err)
				}
				for _, bad := range []*ConfiguredTrust{nil, {}} {
					if ValidateProvisioningConfiguredTrust(node.AuthorityJSON, bundle.Operator.ControlManifest, target, bad) == nil {
						t.Fatal("missing current intent passed preflight")
					}
				}
			}
			if ValidateProvisioningConfiguredTrust(bundle.Nodes[0].AuthorityJSON, bundle.Operator.ControlManifest, "windows", desired) == nil {
				t.Fatal("unsupported target passed configured preflight")
			}
			for _, node := range options.Nodes {
				for _, path := range []string{node.StateDir, node.RenewableDir} {
					if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("pure preflight created state path", path, err)
					}
				}
			}
		})
	}
}

func TestCurrentProvisioningInitialConfiguredTrustPreservesHistoricalSeed(t *testing.T) {
	options := provisioningTestOptions(t, 1, "linux")
	options.Image.Issuers[0].EnvOwned = false
	bundle, err := GenerateProvisioning(options)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := NewConfiguredTrust([]Issuer{})
	if err != nil {
		t.Fatal(err)
	}
	if err = ValidateProvisioningInitialConfiguredTrust(bundle.Nodes[0].AuthorityJSON, bundle.Operator.ControlManifest, "linux", empty); err != nil {
		t.Fatal("fresh desired empty intent did not preserve managed historical administrator", err)
	}
	collision, err := NewConfiguredTrust(options.Image.Issuers)
	if err != nil {
		t.Fatal(err)
	}
	if err = ValidateProvisioningInitialConfiguredTrust(bundle.Nodes[0].AuthorityJSON, bundle.Operator.ControlManifest, "linux", collision); !errors.Is(err, ErrBootstrapLocked) {
		t.Fatal("fresh configured input appropriated a managed trust object", err)
	}
	options.Image.Issuers[0].EnvOwned = true
	bundle, err = GenerateProvisioning(options)
	if err != nil {
		t.Fatal(err)
	}
	if err = ValidateProvisioningInitialConfiguredTrust(bundle.Nodes[0].AuthorityJSON, bundle.Operator.ControlManifest, "linux", empty); !errors.Is(err, ErrLastAdministrator) {
		t.Fatal("fresh configured input withdrew the last administrator", err)
	}
	for _, node := range options.Nodes {
		if _, err = os.Stat(node.StateDir); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("fresh validation created native paths", err)
		}
	}
}

func TestCurrentProvisioningAuthorityLocationsPureAndBound(t *testing.T) {
	options := provisioningTestOptions(t, 1, "linux")
	bundle, err := GenerateProvisioning(options)
	if err != nil {
		t.Fatal(err)
	}
	d := provisioningTestDocument(t, bundle.Nodes[0].AuthorityJSON)
	stateDir := options.Nodes[0].StateDir
	custodyDir := filepath.Join(filepath.Dir(stateDir), "separate-custody")
	renewableDir := options.Nodes[0].RenewableDir
	// Genuine external material may retain its own family names and a separate
	// custody directory. The validator accepts only their already-declared roots.
	d.Participant.PPath = filepath.Join(stateDir, "retained-p")
	d.Participant.BPath = filepath.Join(stateDir, "retained-b")
	d.Membership.Path = filepath.Join(stateDir, "retained-m")
	d.FloorsFile = filepath.Join(custodyDir, "retained-floors.json")
	raw := currentJSONEncode(t, d)
	if err = ValidateProvisioningAuthorityLocations(raw, stateDir, custodyDir, renewableDir); err != nil {
		t.Fatal("valid immutable external locations", err)
	}
	if ValidateProvisioningAuthorityPaths(raw, stateDir, renewableDir) == nil {
		t.Fatal("generated writer assertion accepted external names")
	}
	for name, mutate := range map[string]func(*currentJSONProvisioningDocument){
		"P outside":      func(d *currentJSONProvisioningDocument) { d.Participant.PPath = filepath.Join(custodyDir, "p") },
		"B outside":      func(d *currentJSONProvisioningDocument) { d.Participant.BPath = filepath.Join(custodyDir, "b") },
		"M outside":      func(d *currentJSONProvisioningDocument) { d.Membership.Path = filepath.Join(custodyDir, "m") },
		"floors outside": func(d *currentJSONProvisioningDocument) { d.FloorsFile = filepath.Join(stateDir, "floors") },
		"noncanonical P": func(d *currentJSONProvisioningDocument) { d.Participant.PPath = stateDir + "/child/../p" },
		"relative B":     func(d *currentJSONProvisioningDocument) { d.Participant.BPath = "relative" },
		"family alias":   func(d *currentJSONProvisioningDocument) { d.Participant.BPath = d.Participant.PPath },
		"tip alias":      func(d *currentJSONProvisioningDocument) { d.Participant.BPath = d.Participant.PPath + ".tip" },
		"lease alias":    func(d *currentJSONProvisioningDocument) { d.Membership.Path = d.Participant.PPath + ".lease" },
		"manifest destination": func(d *currentJSONProvisioningDocument) {
			d.Membership.ManifestFile = filepath.Join(renewableDir, "other.json")
		},
		"version": func(d *currentJSONProvisioningDocument) { d.Version++ },
	} {
		t.Run(name, func(t *testing.T) {
			bad := d
			mutate(&bad)
			if ValidateProvisioningAuthorityLocations(currentJSONEncode(t, bad), stateDir, custodyDir, renewableDir) == nil {
				t.Fatal("incompatible immutable location accepted")
			}
		})
	}
	if ValidateProvisioningAuthorityLocations(raw, "relative", custodyDir, renewableDir) == nil || ValidateProvisioningAuthorityLocations(raw, stateDir, "/", renewableDir) == nil {
		t.Fatal("invalid declared roots accepted")
	}
	if !bytes.Equal(raw, currentJSONEncode(t, d)) {
		t.Fatal("location validation rebased immutable document")
	}
	for _, path := range []string{stateDir, custodyDir, renewableDir} {
		if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("pure location validation created a path", path, err)
		}
	}
}

func TestCurrentProvisioningObserverWithoutOriginKeyPureValidation(t *testing.T) {
	options := provisioningTestOptions(t, 3, "linux")
	bundle, err := GenerateProvisioning(options)
	if err != nil {
		t.Fatal(err)
	}
	originDocument := provisioningTestDocument(t, bundle.Nodes[0].AuthorityJSON)
	originDescription, _ := authorityTimeDescriptionForTarget(options.Target)
	originConfig, _, err := currentJSONConfigForTime(originDocument, bundle.Operator.ControlManifest, originDescription, false)
	if err != nil || !validProvisioningOriginKey(originConfig, originDocument.OriginKey) || validProvisioningOriginKey(originConfig, nil) || validProvisioningOriginKey(originConfig, originDocument.Identity.VotingKey) {
		t.Fatal("origin-enabled private key or voter-key separation changed", err)
	}
	document := provisioningTestDocument(t, bundle.Nodes[2].AuthorityJSON)
	unusedOriginKey := bytes.Clone(document.OriginKey)
	document.Genesis.Origins = document.Genesis.Origins[:2]
	description, _ := authorityTimeDescriptionForTarget(options.Target)
	trust, _, err := currentJSONGenesisForTime(document.Genesis, description)
	if err != nil {
		t.Fatal(err)
	}
	document.Membership.Profile.ProtocolScope = trust.scope
	document.Participant.OwnedOrigin = 0
	document.OriginKey = nil
	block, _ := pem.Decode(bundle.Operator.ControlKeyPEM)
	if block == nil {
		t.Fatal("operator key missing from pure construction fixture")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	key := parsed.(ed25519.PrivateKey)
	manifest, err := peerauth.VerifyControlManifest(bundle.Operator.ControlManifest, document.Membership.PublicKey, document.Membership.Profile.Lineage)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Profile = document.Membership.Profile
	signed, err := peerauth.SignControlManifest(manifest, key)
	if err != nil {
		t.Fatal(err)
	}
	raw := currentJSONEncode(t, document)
	desired, err := NewConfiguredTrust([]Issuer{})
	if err != nil {
		t.Fatal(err)
	}
	if err = ValidateProvisioningAuthorityJSON(raw, signed, "linux", document.Genesis.Image); err != nil {
		t.Fatal("origin-free fixed voter failed pure authority validation", err)
	}
	if err = ValidateProvisioningConfiguredTrust(raw, signed, "linux", desired); err != nil {
		t.Fatal("origin-free fixed voter lost its configuration endorsement capability", err)
	}
	if err = ValidateProvisioningInitialConfiguredTrust(raw, signed, "linux", desired); err != nil {
		t.Fatal("managed historical policy with empty desired set failed observer initialization", err)
	}
	profile, err := ProvisioningAuthorityProfile(raw, signed, "linux")
	if err != nil || profile.Protocol != trust.scope {
		t.Fatal("observer profile changed fixed original trust", err)
	}
	c, _, err := currentJSONConfigForTime(document, signed, description, false)
	if err != nil {
		t.Fatal(err)
	}
	if !validProvisioningOriginKey(c, nil) || validProvisioningOriginKey(c, []byte{1}) || validProvisioningOriginKey(c, unusedOriginKey) {
		t.Fatal("observer origin absence borrowed or accepted replacement key material")
	}
	if len(c.Participant.Trust.members) != 3 || len(c.Participant.Trust.origins) != 2 {
		t.Fatal("observer validation changed fixed voter or consume-origin topology")
	}
	for _, node := range options.Nodes {
		for _, path := range []string{node.StateDir, node.RenewableDir} {
			if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("pure observer validation created native paths", path, err)
			}
		}
	}
}
