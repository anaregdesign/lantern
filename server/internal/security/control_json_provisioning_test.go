package security

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func currentJSONProvisioningFixture(t *testing.T) (currentJSONProvisioningDocument, *CurrentProvisioning, *authorityOriginOwner) {
	t.Helper()
	p, origin := currentCustodyFixture(t)
	read := func(path string) []byte {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	var node currentNodeDocument
	if err := json.Unmarshal(read(p.source), &node); err != nil {
		t.Fatal(err)
	}
	var genesis currentGenesisDocument
	if err := json.Unmarshal(read(node.GenesisFile), &genesis); err != nil {
		t.Fatal(err)
	}
	d := currentJSONProvisioningDocument{Version: currentJSONProvisioningVersion, Genesis: genesis, Participant: node.Participant,
		Membership: currentJSONMembershipDocument{node.Membership.Path, node.Membership.ManifestFile, read(node.Membership.KeyFile), node.Membership.Profile, node.Membership.Self},
		Identity:   currentJSONIdentityDocument{string(read(node.Identity.Roots)), string(read(node.Identity.Certificate)), string(read(node.Identity.TLSKey)), read(node.Identity.VotingKey)},
		OriginKey:  read(node.OriginKeyFile), FloorsFile: node.FloorsFile, ListenAddress: node.ListenAddress, Limits: node.Limits}
	return d, p, origin
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

func TestCurrentJSONProvisioningOwnsSourceWithoutPrivateFiles(t *testing.T) {
	d, old, _ := currentJSONProvisioningFixture(t)
	raw := currentJSONEncode(t, d)
	p, err := LoadCurrentProvisioningJSON(raw)
	if err != nil || p.Profile() != old.Profile() || p.binding == old.binding || p.source != "" {
		t.Fatal("JSON source did not preserve independent profile and distinct custody", err)
	}
	var node currentNodeDocument
	nodeRaw, err := os.ReadFile(old.source)
	if err != nil || json.Unmarshal(nodeRaw, &node) != nil {
		t.Fatal("read original test-only input", err)
	}
	for _, path := range []string{old.source, node.GenesisFile, node.Membership.KeyFile, node.Identity.Roots, node.Identity.Certificate, node.Identity.TLSKey, node.Identity.VotingKey, node.OriginKeyFile} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	clear(raw)
	reloaded, err := p.reload()
	if err != nil || reloaded.binding != p.binding || reloaded.Profile() != p.Profile() {
		t.Fatal("owned JSON depended on caller bytes or deleted secret files", err)
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
	if _, err := LoadCurrentProvisioning(old.source); err == nil {
		t.Fatal("deleted file source unexpectedly remained readable")
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

func TestCurrentJSONProvisioningRefusesFileCustodyMigration(t *testing.T) {
	d, fileSource, origin := currentJSONProvisioningFixture(t)
	custody := currentCustodyOpenTest(t, fileSource, "fresh")
	if err := custody.running(); err != nil {
		t.Fatal(err)
	}
	owner := &CurrentAuthority{origin: origin, custody: custody}
	if err := owner.Shutdown(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(fileSource.floors + ".state")
	if err != nil {
		t.Fatal(err)
	}
	jsonSource, err := LoadCurrentProvisioningJSON(currentJSONEncode(t, d))
	if err != nil || jsonSource.Profile() != fileSource.Profile() || jsonSource.binding == fileSource.binding {
		t.Fatal("source identity was silently relabeled", err)
	}
	if _, c, _, err := openCurrentCustody(jsonSource, "resume"); err == nil {
		_ = c.close()
		t.Fatal("JSON source silently migrated original file custody")
	}
	after, err := os.ReadFile(fileSource.floors + ".state")
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("rejected source change altered original CLEAN", err)
	}
	if _, c, _, err := openCurrentCustody(fileSource, "resume"); err != nil {
		t.Fatal("source rejection damaged ordinary original resume", err)
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

func TestCurrentJSONProvisioningOriginalIssuerPolicy(t *testing.T) {
	p, _ := currentCustodyFixture(t)
	image := p.config.Participant.Trust.genesis.state.projection.Image()
	if len(image.Issuers) == 0 {
		t.Fatal("fixture requires original Issuer")
	}
	i := image.Issuers[0]
	i.ConfigRevision++
	i.EnvOwned = !i.EnvOwned
	if err := p.ValidateInitialIssuers([]Issuer{i}); err != nil {
		t.Fatal("input metadata replaced original metadata", err)
	}
	for name, edit := range map[string]func(*Issuer){
		"client":        func(i *Issuer) { i.ClientID += "-changed" },
		"secret handle": func(i *Issuer) { i.SecretRef += "changed" },
		"unknown":       func(i *Issuer) { i.URL = "https://unknown.example" },
		"disabled":      func(i *Issuer) { i.Enabled = !i.Enabled },
	} {
		t.Run(name, func(t *testing.T) {
			copy := i
			edit(&copy)
			if p.ValidateInitialIssuers([]Issuer{copy}) == nil {
				t.Fatal("runtime input changed original Issuer trust")
			}
		})
	}
	if p.ValidateInitialIssuers([]Issuer{i, i}) == nil || (&CurrentProvisioning{}).ValidateInitialIssuers(nil) == nil {
		t.Fatal("duplicate or unprovisioned issuer input accepted")
	}
}
