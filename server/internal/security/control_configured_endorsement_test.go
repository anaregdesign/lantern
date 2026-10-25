package security

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/keyspace"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

func configuredTestEndorsements(f *s2cTestFixture, keys map[uint32]ed25519.PrivateKey, statement configuredTrustStatement, count int) []configuredTrustEndorsement {
	votes := make([]configuredTrustEndorsement, count)
	for n := range votes {
		vote := configuredTrustEndorsement{Member: f.members[n].ID, NotBefore: configuredTestUTC.Add(-time.Second), ExpiresAt: configuredTestUTC.Add(10 * time.Second)}
		vote.Signature = ed25519.Sign(f.keys[vote.Member], configuredEndorsementVoteBody(statement, vote))
		votes[n] = vote
	}
	return votes
}

func TestConfiguredEndorsementFixedMajorityAndDistinctMembers(t *testing.T) {
	for _, count := range []int{1, 3} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			f, keys, desired := configuredTestFixture(t, count)
			header, _ := configuredTestHeader(t, f, keys, desired)
			statement := configuredStatement(header)
			votes := configuredTestEndorsements(f, keys, statement, f.trust.majority())
			if !verifyConfiguredEndorsements(f.trust, statement, votes) || !verifyConfiguredEndorsementWindow(votes, configuredTestUTC, configuredTestUTC.Add(time.Millisecond)) {
				t.Fatal("genuine fixed-origin majority refused")
			}
			if verifyConfiguredEndorsements(f.trust, statement, votes[:f.trust.majority()-1]) {
				t.Fatal("minority changed configured trust authority")
			}
		})
	}
	f, keys, desired := configuredTestFixture(t, 3)
	header, _ := configuredTestHeader(t, f, keys, desired)
	statement := configuredStatement(header)
	votes := configuredTestEndorsements(f, keys, statement, 2)
	if verifyConfiguredEndorsements(f.trust, statement, []configuredTrustEndorsement{votes[0], votes[0]}) {
		t.Fatal("duplicate fixed member counted twice")
	}
	if verifyConfiguredEndorsements(f.trust, statement, []configuredTrustEndorsement{votes[1], votes[0]}) {
		t.Fatal("noncanonical endorsement order accepted")
	}
	// Origin topology does not determine the fixed-voter endorsement quorum.
	// Observers retain their fixed voting identity without an origin signer.
	f.origins[1].Member = f.origins[0].Member
	var err error
	f.trust, err = newS2CTrust(s2cBootstrap{f.genesis, f.members, f.origins, s2cTestBounds()})
	if err != nil {
		t.Fatal(err)
	}
	header, _ = configuredTestHeader(t, f, keys, desired)
	statement = configuredStatement(header)
	votes = configuredTestEndorsements(f, keys, statement, 2)
	if !verifyConfiguredEndorsements(f.trust, statement, votes) {
		t.Fatal("origin ownership changed the independent fixed-voter quorum")
	}
}

func TestConfiguredEndorsementBindsStatementOriginAndWindow(t *testing.T) {
	f, keys, desired := configuredTestFixture(t, 3)
	header, _ := configuredTestHeader(t, f, keys, desired)
	statement := configuredStatement(header)
	votes := configuredTestEndorsements(f, keys, statement, 2)
	for name, mutate := range map[string]func(*configuredTrustStatement){
		"version":     func(s *configuredTrustStatement) { s.Version = 0 },
		"scope":       func(s *configuredTrustStatement) { s.Scope[0] ^= 1 },
		"profile":     func(s *configuredTrustStatement) { s.TimeProfile[0] ^= 1 },
		"revision":    func(s *configuredTrustStatement) { s.Reviewed.Sequence++ },
		"predecessor": func(s *configuredTrustStatement) { s.Predecessor[0] ^= 1 },
		"desired":     func(s *configuredTrustStatement) { s.Desired[0] ^= 1 },
		"slot":        func(s *configuredTrustStatement) { s.Slot++ },
	} {
		t.Run(name, func(t *testing.T) {
			bad := statement
			mutate(&bad)
			if verifyConfiguredEndorsements(f.trust, bad, votes) {
				t.Fatal("statement field escaped endorsement signature")
			}
		})
	}
	for name, mutate := range map[string]func(*configuredTrustEndorsement){
		"member":            func(v *configuredTrustEndorsement) { v.Member = 0 },
		"other member":      func(v *configuredTrustEndorsement) { v.Member = 3 },
		"signature":         func(v *configuredTrustEndorsement) { v.Signature = slices.Clone(v.Signature); v.Signature[0] ^= 1 },
		"missing signature": func(v *configuredTrustEndorsement) { v.Signature = nil },
		"notbefore":         func(v *configuredTrustEndorsement) { v.NotBefore = v.NotBefore.Add(time.Nanosecond) },
		"expiry":            func(v *configuredTrustEndorsement) { v.ExpiresAt = v.ExpiresAt.Add(time.Nanosecond) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := slices.Clone(votes)
			mutate(&bad[0])
			if verifyConfiguredEndorsements(f.trust, statement, bad) {
				t.Fatal("endorsement field escaped signature")
			}
		})
	}
	for name, mutate := range map[string]func(*configuredTrustEndorsement){
		"zero notbefore": func(v *configuredTrustEndorsement) { v.NotBefore = time.Time{} },
		"zero expiry":    func(v *configuredTrustEndorsement) { v.ExpiresAt = time.Time{} },
		"inverted":       func(v *configuredTrustEndorsement) { v.ExpiresAt = v.NotBefore.Add(-time.Nanosecond) },
		"overlong": func(v *configuredTrustEndorsement) {
			v.ExpiresAt = v.NotBefore.Add(time.Duration(authorityRenewalLifetime) + time.Nanosecond)
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := slices.Clone(votes)
			mutate(&bad[0])
			bad[0].Signature = ed25519.Sign(f.keys[bad[0].Member], configuredEndorsementVoteBody(statement, bad[0]))
			if verifyConfiguredEndorsements(f.trust, statement, bad) {
				t.Fatal("invalid window accepted despite correct native-origin signature")
			}
		})
	}
	for name, bounds := range map[string][2]time.Time{
		"zero":            {},
		"inverted":        {configuredTestUTC.Add(time.Second), configuredTestUTC},
		"before":          {votes[0].NotBefore.Add(-time.Nanosecond), configuredTestUTC},
		"expiry equality": {configuredTestUTC, votes[0].ExpiresAt},
		"expired":         {votes[0].ExpiresAt, votes[0].ExpiresAt.Add(time.Second)},
	} {
		t.Run(name, func(t *testing.T) {
			if verifyConfiguredEndorsementWindow(votes, bounds[0], bounds[1]) {
				t.Fatal("consume interval crossed endorsement window")
			}
		})
	}
	originKey := slices.Clone(votes)
	originKey[0].Signature = ed25519.Sign(keys[1], configuredEndorsementVoteBody(statement, originKey[0]))
	if verifyConfiguredEndorsements(f.trust, statement, originKey) {
		t.Fatal("origin signing key substituted for enrolled fixed-voter capability")
	}
}

// A deterministic native membership store and injected bounded clock suffice;
// no listener, HTTP transport or live external time source is opened here.
func configuredTestEndorser(t *testing.T, n *s2cTestNetwork, keys map[uint32]ed25519.PrivateKey, member uint32) *authorityOriginOwner {
	t.Helper()
	clock, _ := fakeAuthorityTimeOwnerAt(t, configuredTestUTC)
	seed := sha256.Sum256([]byte("configured-test-only-offline-membership-operator"))
	operator := ed25519.NewKeyFromSeed(seed[:])
	profile := peerauth.ControlProfile{Version: 1, Lineage: peerauth.ControlLineage{Deployment: [16]byte{1}, Instance: [16]byte{2}},
		NamespaceFormat: keyspace.Version, ProtocolScope: n.fixture.trust.scope, TrustDigest: [32]byte{3}}
	for _, m := range n.fixture.members {
		profile.Voters = append(profile.Voters, peerauth.ControlVoter{Workload: peerauth.Member{ID: [16]byte{byte(m.ID)}, Identity: fmt.Sprintf("spiffe://configured.test/node-%02d", m.ID),
			SPKI: [32]byte{0x20, byte(m.ID)}, Origin: fmt.Sprintf("https://node-%02d.configured.example:7443", m.ID)}, Voter: m.ID, Key: m.PublicKey, Proposer: m.Proposer})
	}
	manifest, err := peerauth.SignControlManifest(peerauth.ControlManifest{Version: 1, Profile: profile, IssuedAt: configuredTestUTC.Add(-time.Minute), ExpiresAt: configuredTestUTC.Add(time.Minute)}, operator)
	if err != nil {
		t.Fatal(err)
	}
	store, err := peerauth.CreateControlStore(peerauth.ControlStoreOptions{Path: filepath.Join(t.TempDir(), "membership"), Key: operator.Public().(ed25519.PublicKey),
		Profile: profile, Self: profile.Voters[member-1].Workload, Now: func() time.Time { return configuredTestUTC }}, manifest)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	descriptor, known := n.fixture.trust.origin(n.nodes[member].config.OwnedOrigin)
	var originKey ed25519.PrivateKey
	if known {
		originKey = keys[descriptor.ID]
	}
	network := &s3aOwner{kernel: n.nodes[member], membership: store, timeOwner: clock, ctx: t.Context(),
		identity: &s3aIdentity{starts: configuredTestUTC.Add(-time.Minute), expires: configuredTestUTC.Add(time.Hour)}}
	owner := &authorityOriginOwner{network: network, descriptor: descriptor, key: originKey}
	network.origin = owner
	return owner
}

func TestConfiguredEndorserRequiresExactLocallySuppliedIntent(t *testing.T) {
	n, keys, desired := configuredTestNativeCluster(t, 3)
	owner := configuredTestEndorser(t, n, keys, 1)
	header, _ := configuredTestHeader(t, n.fixture, keys, desired)
	statement := configuredStatement(header)
	k := owner.network.kernel
	before := k.p.floor
	k.gate.Lock()
	vote, err := owner.endorseConfiguredLocked(statement)
	k.gate.Unlock()
	if err != nil || len(vote.Signature) != ed25519.SignatureSize || k.p.floor != before || k.replayState.slot != 0 {
		t.Fatal("native endorser failed or endorsement materialized state", err)
	}
	member, _ := k.trust.member(k.config.Member)
	if vote.Member != member.ID || !ed25519.Verify(ed25519.PublicKey(member.PublicKey[:]), configuredEndorsementVoteBody(statement, vote), vote.Signature) {
		t.Fatal("endorser did not sign exact bounded statement")
	}
	for name, mutate := range map[string]func(*configuredTrustStatement){
		"version":  func(s *configuredTrustStatement) { s.Version = 0 },
		"scope":    func(s *configuredTrustStatement) { s.Scope[0] ^= 1 },
		"profile":  func(s *configuredTrustStatement) { s.TimeProfile[0] ^= 1 },
		"revision": func(s *configuredTrustStatement) { s.Reviewed.Sequence++ },
		"prefix":   func(s *configuredTrustStatement) { s.Predecessor[0] ^= 1 },
		"desired":  func(s *configuredTrustStatement) { s.Desired[0] ^= 1 },
		"slot":     func(s *configuredTrustStatement) { s.Slot++ },
	} {
		t.Run(name, func(t *testing.T) {
			bad := statement
			mutate(&bad)
			k.gate.Lock()
			_, err := owner.endorseConfiguredLocked(bad)
			k.gate.Unlock()
			if !errors.Is(err, ErrPermissionDenied) || k.p.floor != before {
				t.Fatal("endorser accepted incompatible startup intent or wrote state", err)
			}
		})
	}
	original := k.config.configured
	k.config.configured = nil
	k.gate.Lock()
	_, err = owner.endorseConfiguredLocked(statement)
	k.gate.Unlock()
	k.config.configured = original
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatal("missing local intent borrowed retained configuration", err)
	}
	// The private response also requires the admitted transport and live caller.
	raw, _ := json.Marshal(statement)
	if _, err := owner.network.configuredEndorsementResponse(t.Context(), raw, func() error { return ErrPermissionDenied }); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatal("private response bypassed authenticated transport", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := owner.network.configuredEndorsementResponse(ctx, raw, func() error { return nil }); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatal("cancelled caller received endorsement", err)
	}
	if _, err := owner.network.configuredEndorsementResponse(t.Context(), []byte(`{"Version":1,"Version":1}`), func() error { return nil }); err == nil {
		t.Fatal("duplicate JSON fields accepted by private endorsement endpoint")
	}
	response, err := owner.network.configuredEndorsementResponse(t.Context(), raw, func() error { return nil })
	if err != nil {
		t.Fatal("admitted private endorsement response refused", err)
	}
	var decoded configuredTrustEndorsement
	if s2StrictJSON(response, &decoded, DefaultPolicyLimits()) != nil || !ed25519.Verify(ed25519.PublicKey(member.PublicKey[:]), configuredEndorsementVoteBody(statement, decoded), decoded.Signature) {
		t.Fatal("private response did not retain exact signed native assertion")
	}
	owner.network.timeOwner.failed = true
	k.gate.Lock()
	_, err = owner.endorseConfiguredLocked(statement)
	k.gate.Unlock()
	if err == nil || k.p.floor != before {
		t.Fatal("native endorsement continued after trusted time failed or wrote state", err)
	}
}

func TestConfiguredCapabilityRequiresIndependentEnrollment(t *testing.T) {
	f, _, _ := configuredTestFixture(t, 1)
	d := f.origins[0]
	if configuredTrustCapability(f.trust, d) == [32]byte{} {
		t.Fatal("enrolled native capability missing")
	}
	for name, mutate := range map[string]func(*s2cOriginDescriptor){
		"unknown":       func(d *s2cOriginDescriptor) { d.ID = 0 },
		"zero":          func(d *s2cOriginDescriptor) { *d = s2cOriginDescriptor{} },
		"unenrolled":    func(d *s2cOriginDescriptor) { d.ConfiguredTrust = 0 },
		"wrong version": func(d *s2cOriginDescriptor) { d.ConfiguredTrust++ },
		"OIDC profile":  func(d *s2cOriginDescriptor) { d.Profile = s2cTestProfile() },
		"profile":       func(d *s2cOriginDescriptor) { d.Profile.CredentialContract[0] ^= 1 },
		"key":           func(d *s2cOriginDescriptor) { d.PublicKey[0] ^= 1 },
		"namespace":     func(d *s2cOriginDescriptor) { d.Namespace = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			bad := d
			mutate(&bad)
			if configuredTrustCapability(f.trust, bad) != [32]byte{} {
				t.Fatal("decoded or altered descriptor constructed configuration capability")
			}
		})
	}
	if configuredTrustCapability(nil, d) != [32]byte{} {
		t.Fatal("nil independent trust constructed capability")
	}
}

func TestConfiguredVoterCapabilityRequiresIndependentEnrollment(t *testing.T) {
	f, _, _ := configuredTestFixture(t, 3)
	member := f.members[0]
	if configuredVoterCapability(f.trust, member) == [32]byte{} {
		t.Fatal("enrolled fixed-voter configuration capability missing")
	}
	for name, mutate := range map[string]func(*s2cMember){
		"zero":       func(m *s2cMember) { *m = s2cMember{} },
		"unknown":    func(m *s2cMember) { m.ID = 900 },
		"unenrolled": func(m *s2cMember) { m.ConfiguredTrust = 0 },
		"version":    func(m *s2cMember) { m.ConfiguredTrust++ },
		"key":        func(m *s2cMember) { m.PublicKey[0] ^= 1 },
	} {
		t.Run(name, func(t *testing.T) {
			bad := member
			mutate(&bad)
			if configuredVoterCapability(f.trust, bad) != [32]byte{} {
				t.Fatal("untrusted member descriptor created configuration endorsement capability")
			}
		})
	}
	if configuredVoterCapability(nil, member) != [32]byte{} {
		t.Fatal("nil independent trust created fixed-voter capability")
	}
}

func TestConfiguredObserverEndorsesWithNativeVoterCustody(t *testing.T) {
	f, originKeys, desired := configuredTestFixture(t, 3)
	c := s2cTestConfig(t, f, 3, t.TempDir())
	c.configured, c.OwnedOrigin = desired, 0
	node, err := createS2CParticipant(c)
	if err != nil {
		t.Fatal("fresh observer participant", err)
	}
	t.Cleanup(func() { _ = node.Close() })
	n := &s2cTestNetwork{t: t, fixture: f, configs: map[uint32]s2cParticipantConfig{3: c}, nodes: map[uint32]*s2cParticipant{3: node}}
	observer := configuredTestEndorser(t, n, originKeys, 3)
	if observer.descriptor != (s2cOriginDescriptor{}) || len(observer.key) != 0 || len(node.key) != ed25519.PrivateKeySize {
		t.Fatal("observer borrowed an origin signer or lost native voting custody")
	}
	statement := configuredOwnerStatement(node)
	before := node.p.floor
	node.gate.Lock()
	vote, err := observer.endorseConfiguredLocked(statement)
	node.gate.Unlock()
	if err != nil || vote.Member != 3 || node.p.floor != before || node.replayState.slot != 0 {
		t.Fatal("observer could not approve exact local intent without origin custody", err)
	}
	member, _ := f.trust.member(3)
	if !ed25519.Verify(ed25519.PublicKey(member.PublicKey[:]), configuredEndorsementVoteBody(statement, vote), vote.Signature) {
		t.Fatal("observer endorsement was not signed by its enrolled native voter")
	}
	first := configuredTestEndorsements(f, originKeys, statement, 1)[0]
	if !verifyConfiguredEndorsements(f.trust, statement, []configuredTrustEndorsement{first, vote}) {
		t.Fatal("observer's fixed-voter approval did not complete the original majority")
	}
	if verifyConfiguredEndorsements(f.trust, statement, []configuredTrustEndorsement{vote}) {
		t.Fatal("observer approval alone became a fixed majority")
	}
	// The observer must retain the same local desired-input public gate as any
	// other voter; its endorsement capability does not renew an old projection.
	receiver := &authorityRenewalReceiver{kernel: node, clock: observer.network.timeOwner, workloads: [32]byte{9}}
	if _, err := receiver.challenge(); err == nil {
		t.Fatal("observer renewed public authority before desired intent certified")
	}
}
