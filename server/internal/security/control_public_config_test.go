package security

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCurrentProvisioningIndependentGenesisAndStrictProfile(t *testing.T) {
	n, origins, _ := authorityTestComposite(t)
	c := n.configs[1]
	g := n.f.genesis.state
	dir := t.TempDir()
	genesis := currentGenesisDocument{CurrentPublicVersion, g.projection.cut.Domain, g.projection.cut.Cohort, g.projection.cut.Generation,
		g.projection.cut.Fences, g.projection.Image(), g.configuration, n.f.genesis.roots, n.f.members, n.f.origins, n.f.trust.bounds, sha256.Sum256([]byte(authorityTimeProfileDescription))}
	write := func(name string, value any) (string, []byte) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if err = os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		return path, raw
	}
	genesisPath, genesisRaw := write("genesis.json", genesis)
	keyPath, manifestPath := filepath.Join(dir, "membership.pub"), filepath.Join(dir, "membership.signed")
	for path, raw := range map[string][]byte{keyPath: c.Membership.Key, manifestPath: c.Manifest} {
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	p := c.Participant
	node := currentNodeDocument{Version: CurrentPublicVersion, GenesisFile: genesisPath, GenesisSHA256: sha256.Sum256(genesisRaw),
		Participant: currentParticipantDocument{p.Member, p.Incarnation, p.PPath, p.BPath, p.PIdentity, p.PEpoch, p.PPolicy, p.BScope, p.OwnedOrigin, p.PendingBytes, p.PendingCount, p.OutboxBytes},
		Membership:  currentMembershipDocument{c.Membership.Path, keyPath, manifestPath, c.Membership.Profile, c.Membership.Self},
		Identity:    c.Identity, OriginKeyFile: filepath.Join(filepath.Dir(c.Identity.VotingKey), "origin.key"), FloorsFile: filepath.Join(dir, "floors.json"), ListenAddress: n.listeners[1].Addr().String(), Limits: c.Limits}
	path, raw := write("node.json", node)
	provisioned, err := LoadCurrentProvisioning(path)
	if err != nil || provisioned.Profile().Protocol != n.f.trust.scope || provisioned.Profile().Membership != c.Membership.Profile.Digest() {
		t.Fatal("independent original inputs did not reproduce the installed profile", err)
	}
	if provisioned.config.timeOwner != nil || len(provisioned.config.Participant.Key) != 0 || provisioned.config.Membership.Now != nil {
		t.Fatal("provisioning manufactured runtime clock or signer")
	}
	for name, edit := range map[string]func(*currentNodeDocument){
		"legacy version":      func(d *currentNodeDocument) { d.Version = 1 },
		"wrong genesis":       func(d *currentNodeDocument) { d.GenesisSHA256[0] ^= 1 },
		"wrong cohort":        func(d *currentNodeDocument) { d.Participant.BScope.Cohort[0] ^= 1 },
		"wrong member":        func(d *currentNodeDocument) { d.Participant.Member = 99 },
		"another origin":      func(d *currentNodeDocument) { d.Participant.OwnedOrigin = 2 },
		"wrong listener":      func(d *currentNodeDocument) { d.ListenAddress = "127.0.0.1:0" },
		"missing custody":     func(d *currentNodeDocument) { d.FloorsFile = "" },
		"relative custody":    func(d *currentNodeDocument) { d.FloorsFile = "floors.json" },
		"borrowed membership": func(d *currentNodeDocument) { d.Membership.Profile.ProtocolScope[0] ^= 1 },
	} {
		t.Run(name, func(t *testing.T) {
			copy := node
			edit(&copy)
			path, _ := write("bad-node.json", copy)
			if _, err := LoadCurrentProvisioning(path); err == nil {
				t.Fatal("incompatible current-profile input accepted")
			}
		})
	}
	for _, invalid := range [][]byte{append(append([]byte{}, raw[:len(raw)-1]...), []byte(",\"Version\":2}")...), []byte(`{"Version":2,"Ready":true}`)} {
		if err := os.WriteFile(path, invalid, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadCurrentProvisioning(path); err == nil {
			t.Fatal("duplicate/unknown qualification assertion accepted")
		}
	}
	owner := &CurrentAuthority{origin: origins[1]}
	floors, err := owner.ExportFloors()
	if err != nil {
		t.Fatal(err)
	}
	var f s3aFloors
	if json.Unmarshal(floors, &f) != nil || f.M.Binding != provisioned.Profile().Membership || f.P.Index == 0 || f.B.LocalIndex == 0 {
		t.Fatal("export lost independently retained minimum cuts")
	}
}

func TestCurrentProvisioningRefusesMissingResumeBeforeNativeIO(t *testing.T) {
	for _, p := range []*CurrentProvisioning{nil, {}} {
		if _, err := OpenCurrentAuthority(context.Background(), p, "resume", "https://admin.example"); err == nil {
			t.Fatal("zero provisioning became a production owner")
		}
	}
	if (&CurrentAuthority{}).Ready(context.Background()) || (&CurrentAuthority{}).VerificationTime().Year() != 2262 {
		t.Fatal("unopened owner manufactured readiness or wall-time fallback")
	}
}

// Test-only exporter for root public-wire gates. It creates independent original
// inputs, never M/P/B journals, a clock, H, or a serialized authority assertion.
func TestCurrentProvisioningExportPublicFixture(t *testing.T) {
	dir := os.Getenv("LANTERN_CURRENT_FIXTURE_DIR")
	if dir == "" {
		t.Skip("root public-wire fixture export only")
	}
	if !filepath.IsAbs(dir) {
		t.Fatal("absolute task-owned fixture directory required")
	}
	issuerURL := os.Getenv("LANTERN_CURRENT_FIXTURE_ISSUER")
	u, err := url.Parse(issuerURL)
	if err != nil {
		t.Fatal(err)
	}
	hosts := currentFixtureContainerHosts(t)
	issuerIP, addressErr := netip.ParseAddr(u.Hostname())
	if u.Scheme != "https" || u.Hostname() != "127.0.0.1" && (len(hosts) != 3 || addressErr != nil || !issuerIP.IsPrivate()) {
		t.Fatal("loopback or declared private container TLS fixture issuer required")
	}
	n := s3aTestCluster(t, nil)
	if len(hosts) != 0 {
		// One independently provisioned lifetime covers the bounded campaign;
		// restart never rewrites or extends this signed membership.
		n.manifest.IssuedAt = time.Now().UTC().Add(-time.Minute)
		n.manifest.ExpiresAt = time.Now().UTC().Add(time.Hour)
		for id, c := range n.configs {
			c.Membership.Self.Origin = "https://" + hosts[id-1] + ":16380"
			n.configs[id] = c
			for i := range n.manifest.Profile.Voters {
				if n.manifest.Profile.Voters[i].Voter == id {
					n.manifest.Profile.Voters[i].Workload = c.Membership.Self
				}
			}
		}
	}
	image := s1Image()
	callback := sha256.Sum256([]byte(issuerURL))
	image.Issuers = []Issuer{{URL: issuerURL, ConfigRevision: 1, Enabled: true, ClientID: "admin", APIAudience: "api", RedirectURI: fmt.Sprintf("https://admin.example/auth/callback/%x", callback), Algorithms: []string{"EdDSA"}, HumanSubjectNamespaceQualified: true}}
	for i := range image.Principals {
		image.Principals[i].Identity.Issuer = issuerURL
	}
	// Explicit ordinary data grants, independent of security.manage.
	role := Role{ID: "wire_data"}
	for i, action := range []Action{VertexRead, VertexWrite, VertexDelete, ReceiptRead, Export, CDCIdentity, CDCValue} {
		rule := dataRule(Allow, action, "orders:")
		rule.ID = fmt.Sprintf("data%d", i)
		role.Rules = append(role.Rules, rule)
	}
	image.Roles = append(image.Roles, role)
	image.Principals[0].Assignments = append(image.Principals[0].Assignments, RoleAssignment{RoleID: role.ID})
	reader := Role{ID: "wire_reader", Rules: []PermissionRule{dataRule(Allow, VertexRead, "orders:"), dataRule(Deny, VertexRead, "orders:private:")}}
	reader.Rules[0].ID, reader.Rules[1].ID = "read", "deny"
	blocked := Role{ID: "blocked_admin", Rules: []PermissionRule{{ID: "allow", Effect: Allow, Action: SecurityManage, Resource: GlobalResource}, {ID: "deny", Effect: Deny, Action: SecurityManage, Resource: GlobalResource}}}
	image.Roles = append(image.Roles, reader, blocked)
	for _, entry := range []struct{ subject, role string }{{"reader", reader.ID}, {"denied-manager", blocked.ID}} {
		image.Principals = append(image.Principals, Principal{Identity: Identity{Kind: OIDCPrincipal, Issuer: issuerURL, Subject: entry.subject}, State: Active, HumanIssuerConfigRevision: 1, Assignments: []RoleAssignment{{RoleID: entry.role}}})
	}
	machineToken, err := NewMachineToken()
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := MachineTokenDigest(machineToken)
	machine := Identity{Kind: MachinePrincipal, MachineName: "public-worker"}
	image.Principals = append(image.Principals, Principal{Identity: machine, State: Active, Assignments: []RoleAssignment{{RoleID: "security_admin"}, {RoleID: role.ID}}})
	image.MachineCredentials = []MachineCredential{{Identity: machine, Digest: digest, CreatedAt: time.Now().Add(-time.Minute).UTC(), ExpiresAt: time.Now().Add(time.Hour).UTC()}}
	if err := os.WriteFile(filepath.Join(dir, "machine.token"), []byte(machineToken), 0600); err != nil {
		t.Fatal(err)
	}
	f := s2cTestClusterState(t, 3, s1Fixture(t, image))
	f.origins = f.origins[:2]
	f, keys := authorityTestFixtureState(t, f)
	n.manifest.Profile.ProtocolScope = f.trust.scope
	manifest := n.sign(n.manifest)
	write := func(path string, raw []byte) {
		t.Helper()
		f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.Write(raw); e != nil {
			t.Fatal(e)
		}
		if e = f.Close(); e != nil {
			t.Fatal(e)
		}
	}
	doc := func(path string, value any) []byte {
		t.Helper()
		raw, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		write(path, raw)
		return raw
	}
	g := f.genesis.state
	genesis := currentGenesisDocument{CurrentPublicVersion, g.projection.cut.Domain, g.projection.cut.Cohort, g.projection.cut.Generation, g.projection.cut.Fences, g.projection.Image(), g.configuration, f.genesis.roots, f.members, f.origins, f.trust.bounds, sha256.Sum256([]byte(authorityTimeProfileDescription))}
	genesisPath := filepath.Join(dir, "genesis.json")
	genesisRaw := doc(genesisPath, genesis)
	for id := uint32(1); id <= 3; id++ {
		old := n.configs[id]
		nodeDir := filepath.Join(dir, fmt.Sprintf("node-%d", id))
		if err := os.Mkdir(nodeDir, 0700); err != nil {
			t.Fatal(err)
		}
		p := s2cTestConfig(t, f, id, nodeDir)
		identity := s3aIdentityFiles{Roots: filepath.Join(nodeDir, "roots.pem"), Certificate: filepath.Join(nodeDir, "cert.pem"), TLSKey: filepath.Join(nodeDir, "tls.key"), VotingKey: filepath.Join(nodeDir, "vote.key")}
		for from, to := range map[string]string{old.Identity.Roots: identity.Roots, old.Identity.Certificate: identity.Certificate, old.Identity.TLSKey: identity.TLSKey, old.Identity.VotingKey: identity.VotingKey} {
			raw, e := os.ReadFile(from)
			if e != nil {
				t.Fatal(e)
			}
			if len(hosts) != 0 && from == old.Identity.Certificate {
				block, _ := pem.Decode(raw)
				if block == nil {
					t.Fatal("fixture workload certificate missing")
				}
				leaf, e := x509.ParseCertificate(block.Bytes)
				if e != nil {
					t.Fatal(e)
				}
				leaf.IPAddresses, leaf.DNSNames = []net.IP{net.ParseIP(hosts[id-1])}, nil
				der, e := x509.CreateCertificate(rand.Reader, leaf, n.ca, leaf.PublicKey, n.caKey)
				if e != nil {
					t.Fatal(e)
				}
				raw = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
			}
			write(to, raw)
		}
		keyPath, manifestPath := filepath.Join(nodeDir, "membership.pub"), filepath.Join(nodeDir, "membership.signed")
		write(keyPath, old.Membership.Key)
		write(manifestPath, manifest)
		originPath := ""
		if id <= 2 {
			originPath = filepath.Join(nodeDir, "origin.key")
			write(originPath, keys[id])
		} else {
			p.OwnedOrigin = 0
		}
		node := currentNodeDocument{Version: CurrentPublicVersion, GenesisFile: genesisPath, GenesisSHA256: sha256.Sum256(genesisRaw), Participant: currentParticipantDocument{p.Member, p.Incarnation, p.PPath, p.BPath, p.PIdentity, p.PEpoch, p.PPolicy, p.BScope, p.OwnedOrigin, p.PendingBytes, p.PendingCount, p.OutboxBytes}, Membership: currentMembershipDocument{filepath.Join(nodeDir, "membership"), keyPath, manifestPath, n.manifest.Profile, old.Membership.Self}, Identity: identity, OriginKeyFile: originPath, FloorsFile: filepath.Join(nodeDir, "floors.json"), ListenAddress: n.listeners[id].Addr().String(), Limits: old.Limits}
		if len(hosts) != 0 {
			node.Participant.PPath, node.Participant.BPath = "/journal/protocol.wal", "/journal/materialized.wal"
			node.Membership.Path, node.FloorsFile, node.ListenAddress = "/journal/membership", "/custody/floors.json", "0.0.0.0:16380"
		}
		path := filepath.Join(nodeDir, "node.json")
		doc(path, node)
		if _, err := LoadCurrentProvisioning(path); err != nil {
			t.Fatal("export invalid", id, err)
		}
	}
}

func currentFixtureContainerHosts(t *testing.T) []string {
	t.Helper()
	raw := os.Getenv("LANTERN_CURRENT_FIXTURE_HOSTS")
	if raw == "" {
		return nil
	}
	if os.Getenv("LANTERN_CURRENT_CUSTODY_GATE") != "1" {
		t.Fatal("private container addresses require the explicit custody gate")
	}
	hosts := strings.Split(raw, ",")
	if len(hosts) != 3 {
		t.Fatal("exactly three private fixture addresses required")
	}
	seen := map[string]bool{}
	for _, host := range hosts {
		ip, err := netip.ParseAddr(host)
		if err != nil || !ip.Is4() || !ip.IsPrivate() || ip.String() != host || seen[host] {
			t.Fatal("invalid private fixture address", host)
		}
		seen[host] = true
	}
	return hosts
}
