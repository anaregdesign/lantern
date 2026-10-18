package security

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/membershippublisher"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
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
	canonical, err := currentCustodyPath(manifestPath)
	if err != nil || provisioned.manifest != canonical {
		t.Fatal("manifest path was not retained from validated provisioning", err)
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

func TestCurrentAuthorityReloadMembershipBoundaries(t *testing.T) {
	n := s3aTestCluster(t, nil)
	o := n.start(1) // No quorum and no public readiness.
	path, err := currentCustodyPath(filepath.Join(n.dir, "reload.signed"))
	if err != nil {
		t.Fatal(err)
	}
	a := &CurrentAuthority{origin: &authorityOriginOwner{network: o}, manifest: path}
	install := func(raw []byte) {
		t.Helper()
		tmp := path + ".new"
		if err := os.WriteFile(tmp, raw, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, path); err != nil {
			t.Fatal(err)
		}
	}
	initial, _ := o.membership.Floor()
	for name, raw := range map[string][]byte{"partial": []byte(`{"payload":`), "oversize": bytes.Repeat([]byte("x"), peerauth.MaxManifestBytes+1)} {
		t.Run(name, func(t *testing.T) {
			install(raw)
			if a.ReloadMembership(t.Context()) == nil {
				t.Fatal("invalid file accepted")
			}
			floor, _ := o.membership.Floor()
			if floor != initial {
				t.Fatal("rejected file changed checkpoint")
			}
		})
	}
	install(n.configs[1].Manifest)
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if a.ReloadMembership(t.Context()) == nil {
		t.Fatal("non-private file accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(n.configs[1].Identity.Roots, path); err != nil {
		t.Fatal(err)
	}
	if a.ReloadMembership(t.Context()) == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// Exact replay may return nil after expiry. It cannot renew the interval.
	n.clock.Store(n.manifest.ExpiresAt.Add(time.Second).UnixNano())
	install(n.configs[1].Manifest)
	if err := a.ReloadMembership(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := o.membership.CurrentValidity(t.Context()); err == nil {
		t.Fatal("exact replay renewed expiry")
	}
	next := n.manifest
	next.Version++
	next.IssuedAt = time.Unix(0, n.clock.Load()).UTC().Add(10 * time.Second)
	next.ExpiresAt = next.IssuedAt.Add(time.Minute)
	install(n.sign(next))
	if a.ReloadMembership(t.Context()) == nil {
		t.Fatal("future interval accepted")
	}
	n.clock.Store(next.IssuedAt.Add(time.Second).UnixNano())
	if err := a.ReloadMembership(t.Context()); err != nil {
		t.Fatal("same bytes were not retried after qualification", err)
	}
	good, _ := o.membership.Floor()
	if good.Version != next.Version {
		t.Fatal("higher expired-owner update not applied", good)
	}
	for _, raw := range [][]byte{n.configs[1].Manifest, n.sign(func() peerauth.ControlManifest { m := next; m.ExpiresAt = m.ExpiresAt.Add(time.Second); return m }()), n.sign(func() peerauth.ControlManifest { m := next; m.Version++; m.Profile.Lineage.Instance[0] ^= 1; return m }())} {
		install(raw)
		if a.ReloadMembership(t.Context()) == nil {
			t.Fatal("stale/equivocation/foreign accepted")
		}
		floor, _ := o.membership.Floor()
		if floor != good {
			t.Fatal("rejection changed checkpoint")
		}
	}
	// A valid higher changed profile must reach the terminal fence even if its
	// interval is future; the facade must not prefilter or reparse provisioning.
	next.Version++
	next.Profile.ProtocolScope[0] ^= 1
	next.IssuedAt = next.ExpiresAt.Add(time.Minute)
	next.ExpiresAt = next.IssuedAt.Add(time.Minute)
	install(n.sign(next))
	if a.ReloadMembership(t.Context()) == nil || o.membership.FaultReason() != "control_binding_changed" {
		t.Fatal("changed profile did not fence owner")
	}
	if a.ReloadMembership(t.Context()) == nil {
		t.Fatal("faulted owner recovered")
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if a.ReloadMembership(t.Context()) == nil {
		t.Fatal("closed owner accepted reload")
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
	if u.Scheme != "https" || u.Hostname() != "127.0.0.1" && !(issuerURL == "https://fixture-idp.invalid" && os.Getenv("LANTERN_CURRENT_FIXTURE_BOOTSTRAP_FILE") != "") && (len(hosts) != 3 || addressErr != nil || !issuerIP.IsPrivate()) {
		t.Fatal("loopback or declared private container TLS fixture issuer required")
	}
	count := 3
	if raw := os.Getenv("LANTERN_CURRENT_FIXTURE_VOTERS"); raw != "" {
		count, err = strconv.Atoi(raw)
		if err != nil || count < 1 || count > 31 || strconv.Itoa(count) != raw {
			t.Fatal("original fixed voter count must be 1–31")
		}
	}
	if count != 3 && (len(hosts) != 0 || os.Getenv("LANTERN_NATIVE_FIXTURE_SERVE") == "1" || os.Getenv("LANTERN_CURRENT_MEMBERSHIP_GATE") == "1" || os.Getenv("LANTERN_CURRENT_MEMBERSHIP_CONTAINER") == "1" || os.Getenv("LANTERN_CURRENT_DATA_HA") != "") {
		t.Fatal("the existing container/native campaigns retain their independently provisioned three voters")
	}
	composeOriginal := os.Getenv("LANTERN_AUTH_FIXTURE_COMPOSE") == "1"
	if composeOriginal && (count != 3 || len(hosts) != 0 || os.Getenv("LANTERN_CURRENT_FIXTURE_BOOTSTRAP_FILE") == "") {
		t.Fatal("exact ordinary three-workload Compose original input required")
	}
	n := s3aTestClusterMembers(t, count, nil)
	queryRoles := os.Getenv("LANTERN_CURRENT_FIXTURE_QUERY_ROLES_FILE")
	nativeServing := os.Getenv("LANTERN_NATIVE_FIXTURE_SERVE") == "1"
	servingProfile := os.Getenv("LANTERN_NATIVE_FIXTURE_PROFILE")
	if servingProfile != "" && (!nativeServing || servingProfile != "native" && servingProfile != "physical") {
		t.Fatal("serving profile requires an explicit native or physical serving opt-in")
	}
	physicalServing := nativeServing && servingProfile == "physical"
	if nativeServing && (os.Getenv("LANTERN_NATIVE_LOGIN_GATE") != "1" || queryRoles != "" || len(hosts) != 0) {
		t.Fatal("native serving requires its local native-login fixture")
	}
	containerRenewal := os.Getenv("LANTERN_CURRENT_MEMBERSHIP_CONTAINER") == "1"
	renewal := os.Getenv("LANTERN_CURRENT_MEMBERSHIP_GATE") == "1" || containerRenewal
	if renewal {
		if queryRoles != "" || containerRenewal && len(hosts) != 3 || !containerRenewal && len(hosts) != 0 {
			t.Fatal("renewal gate requires its explicit local or three-container fixture")
		}
		// Native startup can legitimately include its existing 32-second loss
		// backoff. Start the short transition interval only after qualification,
		// using the publisher's first 30-second successor; retain normal bootstrap.
	}
	if queryRoles != "" && len(hosts) != 0 {
		t.Fatal("query fixture is local and cannot select the container campaign")
	}
	if len(hosts) != 0 || queryRoles != "" || nativeServing || composeOriginal || os.Getenv("LANTERN_CURRENT_FIXTURE_BOOTSTRAP_FILE") != "" {
		// One independently provisioned lifetime covers the bounded campaign;
		// ordinary SDK campaigns include setup/crash checks before first use.
		// Restart never rewrites or extends this signed membership.
		n.manifest.IssuedAt = time.Now().UTC().Add(-5 * time.Second)
		n.manifest.ExpiresAt = n.manifest.IssuedAt.Add(peerauth.MaxMembershipLifetime)
	}
	if len(hosts) != 0 {
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
	if composeOriginal {
		for id, c := range n.configs {
			c.Membership.Self.Origin = fmt.Sprintf("https://lantern-%d:16380", id-1)
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
	if os.Getenv("LANTERN_CURRENT_PUBLIC_SDK4") == "1" {
		// Explicit installed-ID fixture for public SDK self inspection; signup
		// itself is qualified separately by the native registration wire gate.
		image.Principals[0].UserUID = "11111111111111111111111111111111"
	}
	if os.Getenv("LANTERN_NATIVE_LOGIN_GATE") == "1" && !physicalServing {
		image, err = currentNativeFixtureImage(image, role, nativeServing, os.Getenv("LANTERN_NATIVE_FIXTURE_ORIGIN"), os.Getenv("LANTERN_NATIVE_FIXTURE_PREFIX"))
		if err != nil {
			t.Fatal(err)
		}
	}
	reader := Role{ID: "wire_reader", Rules: []PermissionRule{dataRule(Allow, VertexRead, "orders:"), dataRule(Deny, VertexRead, "orders:private:")}}
	reader.Rules[0].ID, reader.Rules[1].ID = "read", "deny"
	blocked := Role{ID: "blocked_admin", Rules: []PermissionRule{{ID: "allow", Effect: Allow, Action: SecurityManage, Resource: GlobalResource}, {ID: "deny", Effect: Deny, Action: SecurityManage, Resource: GlobalResource}}}
	image.Roles = append(image.Roles, reader, blocked)
	for _, entry := range []struct{ subject, role string }{{"reader", reader.ID}, {"denied-manager", blocked.ID}} {
		image.Principals = append(image.Principals, Principal{Identity: Identity{Kind: OIDCPrincipal, Issuer: issuerURL, Subject: entry.subject}, State: Active, HumanIssuerConfigRevision: 1, Assignments: []RoleAssignment{{RoleID: entry.role}}})
	}
	if os.Getenv("LANTERN_CURRENT_DATA_HA") != "" {
		blind := Role{ID: "wire_blind", Rules: []PermissionRule{dataRule(Allow, VertexRead, "orders:blind:tail:"), dataRule(Allow, VertexWrite, "orders:blind:"), dataRule(Allow, ReceiptRead, "orders:blind:")}}
		for i := range blind.Rules {
			blind.Rules[i].ID = fmt.Sprintf("blind%d", i)
		}
		image.Roles = append(image.Roles, blind)
		image.Principals = append(image.Principals, Principal{Identity: Identity{Kind: OIDCPrincipal, Issuer: issuerURL, Subject: "blind-writer"}, State: Active, HumanIssuerConfigRevision: 1, Assignments: []RoleAssignment{{RoleID: blind.ID}}})
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
	if physicalServing {
		var credentials currentPhysicalCredentialExport
		image, credentials, err = currentPhysicalFixtureImage(image, machine, machineToken, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(credentials)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "physical-credentials.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if queryRoles != "" {
		image = currentQueryFixtureImage(t, queryRoles, issuerURL, machineToken)
	}
	if path := os.Getenv("LANTERN_CURRENT_FIXTURE_BOOTSTRAP_FILE"); path != "" {
		if queryRoles != "" || nativeServing || renewal || len(hosts) != 0 {
			t.Fatal("ordinary bootstrap fixture cannot alter a separately qualified campaign")
		}
		image = currentOriginalFixtureImage(t, path, issuerURL)
	}
	f := s2cTestClusterState(t, count, s1Fixture(t, image))
	originCount := min(2, count)
	f.origins = f.origins[:originCount]
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
	var queryNodes []map[string]any
	for id := uint32(1); id <= uint32(count); id++ {
		old := n.configs[id]
		nodeDir := filepath.Join(dir, fmt.Sprintf("node-%d", id))
		if err := os.Mkdir(nodeDir, 0700); err != nil {
			t.Fatal(err)
		}
		p := s2cTestConfig(t, f, id, nodeDir)
		p.OutboxBytes = old.Participant.OutboxBytes
		identity := s3aIdentityFiles{Roots: filepath.Join(nodeDir, "roots.pem"), Certificate: filepath.Join(nodeDir, "cert.pem"), TLSKey: filepath.Join(nodeDir, "tls.key"), VotingKey: filepath.Join(nodeDir, "vote.key")}
		for from, to := range map[string]string{old.Identity.Roots: identity.Roots, old.Identity.Certificate: identity.Certificate, old.Identity.TLSKey: identity.TLSKey, old.Identity.VotingKey: identity.VotingKey} {
			raw, e := os.ReadFile(from)
			if e != nil {
				t.Fatal(e)
			}
			if (len(hosts) != 0 || composeOriginal) && from == old.Identity.Certificate {
				block, _ := pem.Decode(raw)
				if block == nil {
					t.Fatal("fixture workload certificate missing")
				}
				leaf, e := x509.ParseCertificate(block.Bytes)
				if e != nil {
					t.Fatal(e)
				}
				if composeOriginal {
					leaf.IPAddresses = nil
					leaf.DNSNames = []string{fmt.Sprintf("lantern-%d", id-1)}
				} else {
					leaf.IPAddresses, leaf.DNSNames = []net.IP{net.ParseIP(hosts[id-1])}, nil
				}
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
		if id <= uint32(originCount) {
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
		if composeOriginal {
			node.GenesisFile = "/run/lantern-config/authority-genesis.json"
			node.Participant.PPath, node.Participant.BPath = "/state/control-protocol.wal", "/state/control-materialized.wal"
			node.Membership.Path, node.FloorsFile, node.ListenAddress = "/state/control-membership", "/state/control-floors.json", "0.0.0.0:16380"
			node.Membership.KeyFile, node.Membership.ManifestFile = "/run/lantern-config/authority-membership.pub", "/run/lantern-config/authority-membership.signed"
			node.Identity = s3aIdentityFiles{Roots: "/run/lantern-config/authority-roots.pem", Certificate: "/run/lantern-config/authority-cert.pem", TLSKey: "/run/lantern-config/authority-tls.key", VotingKey: "/run/lantern-config/authority-vote.key"}
			if node.OriginKeyFile != "" {
				node.OriginKeyFile = "/run/lantern-config/authority-origin.key"
			}
		}
		path := filepath.Join(nodeDir, "node.json")
		doc(path, node)
		if composeOriginal {
			continue
		}
		provisioned, err := LoadCurrentProvisioning(path)
		if err != nil {
			t.Fatal("export invalid", id, err)
		}
		queryNodes = append(queryNodes, map[string]any{"node_file": path, "floors_file": provisioned.floors, "custody_binding": provisioned.binding})
	}
	if composeOriginal {
		der, e := x509.MarshalPKCS8PrivateKey(n.operator)
		if e != nil {
			t.Fatal(e)
		}
		write(filepath.Join(dir, "control-operator.key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
		doc(filepath.Join(dir, "compose-provision.json"), map[string]any{"profile": CurrentProfile{CurrentPublicVersion, genesis.Domain, genesis.Cohort, genesis.Generation, f.trust.scope, genesis.TimeProfile, n.manifest.Profile.Digest(), g.configuration.Digest()}})
	}
	if queryRoles != "" {
		doc(filepath.Join(dir, "query-provision.json"), map[string]any{"membership_expires_at": n.manifest.ExpiresAt, "nodes": queryNodes})
	}
	if nativeServing {
		doc(filepath.Join(dir, "native-serving-provision.json"), map[string]any{"membership_expires_at": n.manifest.ExpiresAt})
	}
	if renewal {
		exportCurrentRenewalFixture(t, dir, n, manifest, hosts)
	}
	if driver := os.Getenv("LANTERN_CURRENT_DATA_HA"); driver != "" {
		provisioned, err := LoadCurrentProvisioning(filepath.Join(dir, "node-1", "node.json"))
		if err != nil || len(hosts) != 3 || net.ParseIP(driver) == nil {
			t.Fatal("data HA requires the current three-container fixture", err)
		}
		exportCurrentDataHA(t, dir, hosts, driver, provisioned.Profile())
	}
}

type currentPhysicalCredentialExport struct {
	ObserverTokens []string  `json:"observer_tokens"`
	WriterToken    string    `json:"writer_token"`
	ExpiresAt      time.Time `json:"expires_at"`
}

func currentPhysicalFixtureImage(image Image, observer Identity, primary string, now time.Time) (Image, currentPhysicalCredentialExport, error) {
	export := currentPhysicalCredentialExport{ObserverTokens: []string{primary}, ExpiresAt: now.Add(time.Hour)}
	for range 2 {
		token, err := NewMachineToken()
		if err != nil {
			return Image{}, currentPhysicalCredentialExport{}, err
		}
		if len(export.ObserverTokens) == 1 {
			export.ObserverTokens = append(export.ObserverTokens, token)
		} else {
			export.WriterToken = token
		}
	}
	reader := Role{ID: "physical_observer"}
	for _, prefix := range []string{"mobile-smoke:", "physical-api:", "physical-cdc:", "physical-contribution:", "physical-receipt:", "physical-head:", "flutter-demo:"} {
		for _, action := range []Action{VertexRead, VertexWrite, VertexDelete, ReceiptRead, CDCIdentity, CDCValue, Query} {
			rule := dataRule(Allow, action, prefix)
			rule.ID = fmt.Sprintf("physical%d", len(reader.Rules))
			reader.Rules = append(reader.Rules, rule)
		}
	}
	writer := Role{ID: "physical_head_writer", Rules: []PermissionRule{
		dataRule(Allow, VertexRead, "physical-head:tails:"),
		dataRule(Allow, VertexWrite, "physical-head:heads:"),
		dataRule(Deny, VertexWrite, "physical-head:heads:denied:"),
		dataRule(Allow, ReceiptRead, "physical-head:"),
	}}
	for i := range writer.Rules {
		writer.Rules[i].ID = fmt.Sprintf("head%d", i)
	}
	image.Roles = append(image.Roles, reader, writer)
	for i := range image.Principals {
		if image.Principals[i].Identity == observer {
			image.Principals[i].Assignments = []RoleAssignment{{RoleID: reader.ID}}
		}
	}
	writerIdentity := Identity{Kind: MachinePrincipal, MachineName: "physical-head-writer"}
	image.Principals = append(image.Principals, Principal{Identity: writerIdentity, State: Active, Assignments: []RoleAssignment{{RoleID: writer.ID}}})
	image.MachineCredentials = nil
	for i, token := range append(append([]string(nil), export.ObserverTokens...), export.WriterToken) {
		digest, valid := MachineTokenDigest(token)
		if !valid {
			return Image{}, currentPhysicalCredentialExport{}, fmt.Errorf("canonical physical machine credential required")
		}
		identity := observer
		if i == 2 {
			identity = writerIdentity
		}
		image.MachineCredentials = append(image.MachineCredentials, MachineCredential{Identity: identity, Digest: digest, CreatedAt: now.Add(-time.Minute), ExpiresAt: export.ExpiresAt})
	}
	return image, export, nil
}

func TestCurrentPhysicalFixtureImage(t *testing.T) {
	observer := Identity{Kind: MachinePrincipal, MachineName: "public-worker"}
	image := s1Image()
	image.Principals = append(image.Principals, Principal{Identity: observer, State: Active, Assignments: []RoleAssignment{{RoleID: "security_admin"}}})
	token, err := NewMachineToken()
	if err != nil {
		t.Fatal(err)
	}
	image, exported, err := currentPhysicalFixtureImage(image, observer, token, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	_ = s1Fixture(t, image)
	if len(exported.ObserverTokens) != 2 || exported.ObserverTokens[0] != token || exported.ObserverTokens[0] == exported.ObserverTokens[1] || exported.WriterToken == exported.ObserverTokens[1] {
		t.Fatal("independent original rotating credentials required")
	}
	if len(image.MachineCredentials) != 3 || image.MachineCredentials[0].Identity != image.MachineCredentials[1].Identity || image.MachineCredentials[1].Identity == image.MachineCredentials[2].Identity {
		t.Fatal("rotation changed the observer or collapsed the Head writer")
	}
	for _, principal := range image.Principals {
		if principal.Identity == observer && (len(principal.Assignments) != 1 || principal.Assignments[0].RoleID != "physical_observer") {
			t.Fatal("physical machine retained management authority")
		}
	}
	reader, writer := image.Roles[len(image.Roles)-2], image.Roles[len(image.Roles)-1]
	if len(reader.Rules) != 49 || len(writer.Rules) != 4 || writer.Rules[2].Effect != Deny || writer.Rules[0].Action != VertexRead || writer.Rules[1].Action != VertexWrite || writer.Rules[3].Action != ReceiptRead {
		t.Fatal("physical scopes or minimal Head authority drifted")
	}
	for _, rule := range reader.Rules {
		if rule.Resource != DataResource || rule.Prefix == nil || *rule.Prefix == "" || strings.HasPrefix(*rule.Prefix, "sys:") {
			t.Fatal("physical observer escaped explicit synthetic prefixes")
		}
	}
}

func currentNativeFixtureImage(image Image, role Role, serving bool, origin, prefix string) (Image, error) {
	if !serving {
		if origin != "" || prefix != "" {
			return Image{}, fmt.Errorf("native origin/prefix override requires serving opt-in")
		}
		origin, prefix = "https://native.example", "orders:"
	} else if !nativeServiceOrigin(origin) || !strings.HasPrefix(prefix, "orders:") || !strings.HasSuffix(prefix, ":") || len(prefix) > 128 || strings.ContainsAny(prefix, " \t\r\n\x00") {
		return Image{}, fmt.Errorf("canonical HTTPS native origin and bounded orders: prefix required")
	}
	nativeRole := Role{ID: "wire_native", Rules: append([]PermissionRule(nil), role.Rules...)}
	if serving {
		for i := range nativeRole.Rules {
			nativeRole.Rules[i].Prefix = &prefix
		}
		query := dataRule(Allow, Query, prefix)
		query.ID = "native-fixture-query"
		nativeRole.Rules = append(nativeRole.Rules, query)
	}
	image.Roles = append(image.Roles, nativeRole)
	image.Issuers = append(image.Issuers,
		Issuer{URL: GoogleIssuer, Enabled: true, ConfigRevision: 1, Algorithms: []string{"RS256"}, NativeClients: []NativeClient{{ID: "google", Kind: GoogleIDToken, ClientID: "fixture.apps.googleusercontent.com", Presenters: []string{"fixture-android", "fixture-ios"}, ServiceOrigin: origin}}},
		Issuer{URL: AppleIssuer, Enabled: true, ConfigRevision: 1, Algorithms: []string{"RS256"}, NativeClients: []NativeClient{{ID: "apple_ios", Kind: AppleNative, ClientID: "com.example.native", ServiceOrigin: origin, SecretRef: "apple_ios"}, {ID: "apple_web", Kind: AppleAndroidWeb, ClientID: "com.example.web", ServiceOrigin: origin, SecretRef: "apple_web", RedirectURI: origin + "/auth/native/apple/callback/apple_web", AndroidPackage: "com.example.native"}}},
	)
	// Fixture-only policy; enrollment itself still crosses public Prepare/Apply.
	image.Roles = append(image.Roles, PersonalUserRole())
	image.RegistrationPolicy = &RegistrationPolicy{Version: RegistrationPolicyVersion, Enabled: true, RoleID: PersonalUserRoleID, Profiles: []RegistrationProfile{
		{GoogleIssuer, "google"}, {AppleIssuer, "apple_ios"}, {AppleIssuer, "apple_web"},
	}}
	image.Principals = append(image.Principals, Principal{Identity: Identity{Kind: OIDCPrincipal, Issuer: GoogleIssuer, Subject: "registration-legacy"}, State: Active, Assignments: []RoleAssignment{{RoleID: nativeRole.ID}}})
	for _, issuer := range []string{GoogleIssuer, AppleIssuer} {
		image.Principals = append(image.Principals, Principal{Identity: Identity{Kind: OIDCPrincipal, Issuer: issuer, Subject: "native-user"}, State: Active, Assignments: []RoleAssignment{{RoleID: nativeRole.ID}}})
	}
	return image, nil
}

func TestCurrentNativeFixtureImage(t *testing.T) {
	role := Role{ID: "wire_data"}
	for i, action := range []Action{VertexRead, VertexWrite, VertexDelete, ReceiptRead, Export, CDCIdentity, CDCValue} {
		rule := dataRule(Allow, action, "orders:")
		rule.ID = fmt.Sprintf("data%d", i)
		role.Rules = append(role.Rules, rule)
	}
	for _, serving := range []bool{false, true} {
		t.Run(fmt.Sprintf("serving=%t", serving), func(t *testing.T) {
			origin, prefix := "", ""
			if serving {
				origin, prefix = "https://installed.example:16443", "orders:installed:"
			}
			image, err := currentNativeFixtureImage(s1Image(), role, serving, origin, prefix)
			if err != nil {
				t.Fatal(err)
			}
			_ = s1Fixture(t, image)
			if !serving {
				origin, prefix = "https://native.example", "orders:"
			}
			nativeRole := image.Roles[len(image.Roles)-2]
			want := len(role.Rules)
			if serving {
				want++
			}
			if nativeRole.ID != "wire_native" || len(nativeRole.Rules) != want {
				t.Fatal("native grants changed")
			}
			for i, rule := range nativeRole.Rules {
				if rule.Prefix == nil || *rule.Prefix != prefix || rule.Effect != Allow || rule.Resource != DataResource || i < len(role.Rules) && rule.Action != role.Rules[i].Action || i == len(role.Rules) && rule.Action != Query {
					t.Fatal("native fixture rule escaped explicit scope")
				}
			}
			for _, issuer := range image.Issuers[1:] {
				for _, client := range issuer.NativeClients {
					if client.ServiceOrigin != origin || !client.valid(issuer.URL) {
						t.Fatal("native origin/callback invalid")
					}
				}
			}
			for _, principal := range image.Principals[1:] {
				if principal.UserUID != "" {
					t.Fatal("fixture bypassed public registration")
				}
			}
			for _, rule := range role.Rules {
				if *rule.Prefix != "orders:" {
					t.Fatal("serving mutated ordinary wire_data grants")
				}
			}
		})
	}
	for _, tc := range []struct {
		serving        bool
		origin, prefix string
	}{{false, "https://installed.example", "orders:"}, {true, "http://installed.example", "orders:"}, {true, "https://installed.example/", "orders:"}, {true, "https://installed.example", ""}, {true, "https://installed.example", "other:"}, {true, "https://installed.example", "orders:bad\n:"}} {
		if _, err := currentNativeFixtureImage(s1Image(), role, tc.serving, tc.origin, tc.prefix); err == nil {
			t.Fatal("invalid native serving configuration accepted")
		}
	}
}

func TestCurrentNativeFixtureExport(t *testing.T) {
	for _, serving := range []bool{false, true} {
		t.Run(fmt.Sprintf("serving=%t", serving), func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("LANTERN_CURRENT_FIXTURE_DIR", dir)
			t.Setenv("LANTERN_CURRENT_FIXTURE_ISSUER", "https://127.0.0.1:12345")
			t.Setenv("LANTERN_CURRENT_FIXTURE_HOSTS", "")
			t.Setenv("LANTERN_CURRENT_FIXTURE_QUERY_ROLES_FILE", "")
			t.Setenv("LANTERN_NATIVE_LOGIN_GATE", "1")
			t.Setenv("LANTERN_NATIVE_FIXTURE_SERVE", "")
			t.Setenv("LANTERN_NATIVE_FIXTURE_ORIGIN", "")
			t.Setenv("LANTERN_NATIVE_FIXTURE_PREFIX", "")
			if serving {
				t.Setenv("LANTERN_NATIVE_FIXTURE_SERVE", "1")
				t.Setenv("LANTERN_NATIVE_FIXTURE_ORIGIN", "https://installed.example")
				t.Setenv("LANTERN_NATIVE_FIXTURE_PREFIX", "orders:installed:")
			}
			TestCurrentProvisioningExportPublicFixture(t)
			raw, err := os.ReadFile(filepath.Join(dir, "native-serving-provision.json"))
			if !serving {
				if !os.IsNotExist(err) {
					t.Fatal("ordinary native export gained serving state")
				}
				return
			}
			var provision struct {
				ExpiresAt time.Time `json:"membership_expires_at"`
			}
			if err != nil || json.Unmarshal(raw, &provision) != nil || time.Until(provision.ExpiresAt) < 9*time.Minute || time.Until(provision.ExpiresAt) > peerauth.MaxMembershipLifetime {
				t.Fatal("native serving expiry escaped the signed membership maximum")
			}
			for id := 1; id <= 3; id++ {
				p, err := LoadCurrentProvisioning(filepath.Join(dir, fmt.Sprintf("node-%d/node.json", id)))
				if err != nil || p.Profile().Binding() == "" {
					t.Fatal("invalid native current-v2 provisioning", err)
				}
			}
		})
	}
}

func exportCurrentRenewalFixture(t *testing.T, dir string, n *s3aTestNetwork, control []byte, hosts []string) {
	t.Helper()
	write := func(path string, raw []byte) {
		t.Helper()
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeKey := func(name string, key ed25519.PrivateKey) string {
		t.Helper()
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		write(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
		return path
	}
	p, err := LoadCurrentProvisioning(filepath.Join(dir, "node-1", "node.json"))
	if err != nil {
		t.Fatal(err)
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	publicPath := filepath.Join(dir, "data-operator.pub")
	write(publicPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}))
	m := peerauth.Manifest{Version: 1, Domain: peerauth.Domain{Deployment: [16]byte{99}, NamespaceFormat: "namespaced-v1", AuthMode: "oidc", SecurityGeneration: p.profile.Generation, CurrentProfile: p.profile.Binding(), TrustDigest: n.manifest.Profile.TrustDigest}, IssuedAt: n.manifest.IssuedAt, ExpiresAt: n.manifest.ExpiresAt}
	var controlTargets, dataTargets, certificates []string
	for id := 1; id <= 3; id++ {
		node := filepath.Join(dir, fmt.Sprintf("node-%d", id))
		controlTargets = append(controlTargets, filepath.Join(node, "membership.signed"))
		input := filepath.Join(node, "data-input")
		if err := os.Mkdir(input, 0700); err != nil {
			t.Fatal(err)
		}
		dataTargets = append(dataTargets, filepath.Join(input, "data.signed"))
		certificates = append(certificates, filepath.Join(node, "cert.pem"))
		member := n.manifest.Profile.Voters[id-1].Workload
		if len(hosts) != 0 {
			member.Origin = "https://" + hosts[id-1] + ":17380"
		}
		m.Members = append(m.Members, member)
	}
	data, err := peerauth.SignManifest(m, key)
	if err != nil {
		t.Fatal(err)
	}
	for i, path := range dataTargets {
		write(path, data)
		node := filepath.Dir(filepath.Dir(path))
		peer := map[string]any{"ListenAddress": fmt.Sprintf("127.0.0.1:%d", 17441+i), "Identity": map[string]any{"AuthMode": "oidc", "Deployment": m.Domain.Deployment, "SecurityGeneration": m.Domain.SecurityGeneration, "CurrentProfile": m.Domain.CurrentProfile, "CAFile": filepath.Join(node, "roots.pem"), "OperatorKeyFile": publicPath, "ManifestFile": path, "StateFile": filepath.Join(node, "data-membership"), "StateMode": "fresh", "CertFile": certificates[i], "KeyFile": filepath.Join(node, "tls.key"), "SelfIdentity": m.Members[i].Identity}}
		if len(hosts) != 0 {
			peer["ListenAddress"] = "0.0.0.0:17380"
			peer["Identity"].(map[string]any)["StateFile"] = "/journal/data-membership"
		}
		raw, err := json.Marshal(peer)
		if err != nil {
			t.Fatal(err)
		}
		write(filepath.Join(node, "peer.json"), raw)
	}
	roots := filepath.Join(dir, "node-1", "roots.pem")
	c := membershippublisher.Config{Version: 1, End: time.Now().UTC().Add(3 * time.Minute), CleanupBy: time.Now().UTC().Add(4 * time.Minute), Interval: 8 * time.Second, Lifetime: 30 * time.Second, Streams: []membershippublisher.Stream{
		{Kind: "control", KeyFile: writeKey("control-operator.key", n.operator), RootsFile: roots, Initial: control, Targets: controlTargets, Certificates: certificates},
		{Kind: "data", KeyFile: writeKey("data-operator.key", key), RootsFile: roots, Initial: data, Targets: dataTargets, Certificates: certificates},
	}}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(dir, "publisher.json"), raw)
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

// Only the private query exporter selects this original genesis image. It adds
// the existing bench actors/roles, never an injected authority or clock.
func currentQueryFixtureImage(t *testing.T, path, issuer, machineToken string) Image {
	t.Helper()
	var roles []Role
	raw, err := os.ReadFile(path)
	if err != nil || !filepath.IsAbs(path) || len(raw) > 64<<10 {
		t.Fatal("bounded original query roles required", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&roles); err != nil {
		t.Fatal(err)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || len(roles) != 2 || roles[0].ID != "fixture_data" || roles[1].ID != "fixture_query_reader" {
		t.Fatal("exact query fixture roles required")
	}
	image := s1Image()
	callback := sha256.Sum256([]byte(issuer))
	image.Issuers = []Issuer{{URL: issuer, ConfigRevision: 1, Enabled: true, ClientID: "fixture-query-client", APIAudience: "lantern-fixture", RedirectURI: fmt.Sprintf("https://admin.example/auth/callback/%x", callback), Algorithms: []string{"EdDSA"}, HumanSubjectNamespaceQualified: true}}
	admin := image.Principals[0]
	admin.Identity.Issuer, admin.Identity.Subject = issuer, "fixture-admin"
	image.Roles = append(image.Roles, roles...)
	reader := Principal{Identity: Identity{Kind: OIDCPrincipal, Issuer: issuer, Subject: "fixture-query-reader"}, State: Active, HumanIssuerConfigRevision: 1, Assignments: []RoleAssignment{{RoleID: "fixture_query_reader"}}}
	machine := Identity{Kind: MachinePrincipal, MachineName: "fixture-client"}
	digest, valid := MachineTokenDigest(machineToken)
	if !valid {
		t.Fatal("canonical query machine token required")
	}
	image.Principals = []Principal{admin, reader, {Identity: machine, State: Active, Assignments: []RoleAssignment{{RoleID: "fixture_data"}}}}
	image.MachineCredentials = []MachineCredential{{Identity: machine, Digest: digest, CreatedAt: time.Now().Add(-time.Minute).UTC(), ExpiresAt: time.Now().Add(time.Hour).UTC()}}
	return image
}

func TestCurrentQueryFixtureImageRetainsActorsAndDeny(t *testing.T) {
	path := currentQueryRoleTestFile(t)
	hidden := "bench:private:"
	token, err := NewMachineToken()
	if err != nil {
		t.Fatal(err)
	}
	image := currentQueryFixtureImage(t, path, "https://127.0.0.1:12345", token)
	if len(image.Principals) != 3 || image.Principals[0].Identity.Subject != "fixture-admin" || image.Principals[1].Identity.Subject != "fixture-query-reader" || image.Principals[2].Identity.MachineName != "fixture-client" || image.Issuers[0].APIAudience != "lantern-fixture" {
		t.Fatal("query actors changed")
	}
	got := image.Roles[len(image.Roles)-1]
	if got.ID != "fixture_query_reader" || got.Rules[1].Effect != Deny || *got.Rules[1].Prefix != hidden {
		t.Fatal("Deny changed")
	}
	_ = s1Fixture(t, image)
}

func TestCurrentQueryFixtureExportRetainsOriginalFiles(t *testing.T) {
	path := currentQueryRoleTestFile(t)
	exportDir := filepath.Join(t.TempDir(), "original")
	if err := os.Mkdir(exportDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LANTERN_CURRENT_FIXTURE_DIR", exportDir)
	t.Setenv("LANTERN_CURRENT_FIXTURE_ISSUER", "https://127.0.0.1:12345")
	t.Setenv("LANTERN_CURRENT_FIXTURE_QUERY_ROLES_FILE", path)
	t.Setenv("LANTERN_CURRENT_FIXTURE_HOSTS", "")
	TestCurrentProvisioningExportPublicFixture(t)
	rawManifest, err := os.ReadFile(filepath.Join(exportDir, "query-provision.json"))
	if err != nil {
		t.Fatal(err)
	}
	var exported struct {
		Nodes []struct {
			NodeFile   string   `json:"node_file"`
			FloorsFile string   `json:"floors_file"`
			Binding    [32]byte `json:"custody_binding"`
		} `json:"nodes"`
		ExpiresAt time.Time `json:"membership_expires_at"`
	}
	if json.Unmarshal(rawManifest, &exported) != nil || len(exported.Nodes) != 3 || time.Until(exported.ExpiresAt) < time.Minute {
		t.Fatal("original query export incomplete")
	}
	for _, node := range exported.Nodes {
		p, err := LoadCurrentProvisioning(node.NodeFile)
		if err != nil || p.binding != node.Binding || p.floors != node.FloorsFile || p.Profile().Binding() == "" {
			t.Fatal("original node binding mismatch", err)
		}
		if _, err := os.Stat(node.FloorsFile); !os.IsNotExist(err) {
			t.Fatal("authoring created runtime custody")
		}
	}

}

func currentQueryRoleTestFile(t *testing.T) string {
	t.Helper()
	prefix, hidden, all := "bench:", "bench:private:", ""
	roles := []Role{{ID: "fixture_data", Rules: []PermissionRule{{ID: "write", Action: VertexWrite, Effect: Allow, Resource: DataResource, Prefix: &all}}}, {ID: "fixture_query_reader", Rules: []PermissionRule{{ID: "read", Action: VertexRead, Effect: Allow, Resource: DataResource, Prefix: &prefix}, {ID: "deny", Action: VertexRead, Effect: Deny, Resource: DataResource, Prefix: &hidden}, {ID: "query", Action: Query, Effect: Allow, Resource: DataResource, Prefix: &all}}}}
	raw, err := json.Marshal(roles)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "roles.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
