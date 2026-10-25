package security

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// These fixtures enroll configuration origins in the original independent
// trust, before opening native state. They never upgrade a running genesis.
func configuredTestFixture(t *testing.T, count int) (*s2cTestFixture, map[uint32]ed25519.PrivateKey, *ConfiguredTrust) {
	t.Helper()
	f, keys := authorityTestFixture(t, count)
	configuredTestEnroll(t, f)
	issuer := f.genesis.state.projection.Image().Issuers[0].clone()
	issuer.URL = "https://configured.example"
	desired, err := NewConfiguredTrust([]Issuer{issuer})
	if err != nil {
		t.Fatal(err)
	}
	return f, keys, desired
}

func configuredTestEnroll(t *testing.T, f *s2cTestFixture) {
	t.Helper()
	for n := range f.members {
		f.members[n].ConfiguredTrust = configuredTrustVersion
	}
	for n := range f.origins {
		f.origins[n].ConfiguredTrust = configuredTrustVersion
	}
	members, err := s2cMemberSetDigest(f.members)
	if err != nil {
		t.Fatal(err)
	}
	old := f.genesis.state
	retention := S1Retention{}
	if old.retiredThrough != 0 {
		retention = S1Retention{old.retiredThrough, old.projection.cut.Domain, old.projection.cut.Cohort, f.genesis.roots.Retirement}
	}
	state, err := NewS1ApplyState(old.projection, members, old.configuration.Capacity, retention)
	if err != nil {
		t.Fatal(err)
	}
	f.genesis = &s2TrustedGenesis{state: state, roots: f.genesis.roots}
	f.trust, err = newS2CTrust(s2cBootstrap{f.genesis, f.members, f.origins, s2cTestBounds()})
	if err != nil {
		t.Fatal(err)
	}
}

func configuredTestHeader(t *testing.T, f *s2cTestFixture, keys map[uint32]ed25519.PrivateKey, desired *ConfiguredTrust) (configuredTrustHeader, OperationIdentity) {
	t.Helper()
	s := f.genesis.state
	op, err := newConfiguredOperation(s.projection.cut, desired)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := f.trust.origin(1)
	handoff := &S1Handoff{id: FullChangeID{S1Version, op.reviewed.Domain, op.reviewed.Cohort, d.Namespace, [16]byte{0xc1}}, operation: op,
		origin: f.trust.originDigests[d.ID], serial: 1, configured: &authorityConfiguredAuthorization{desired: desired, predecessor: s.prefix, slot: s.slot, capability: configuredTrustCapability(f.trust, d)}}
	h := configuredTrustHeader{Version: configuredTrustVersion, Scope: f.trust.scope, OriginID: d.ID, OriginDigest: handoff.origin, Incarnation: d.Incarnation,
		ID: handoff.id, Serial: handoff.serial, Reviewed: op.reviewed, Predecessor: s.prefix, Desired: desired.Digest(), Operation: op.digest, Value: handoff.digest(), Slot: s.slot,
		ConsumeLow: configuredTestUTC, ConsumeHigh: configuredTestUTC.Add(time.Millisecond)}
	h.Endorsements = configuredTestEndorsements(f, keys, configuredStatement(h), f.trust.majority())
	return h, op
}

func configuredTestSeal(t *testing.T, f *s2cTestFixture, key ed25519.PrivateKey, h configuredTrustHeader, op OperationIdentity) []byte {
	t.Helper()
	body, err := configuredTrustBody(h, op.Encode(), f.trust.bounds)
	if err != nil {
		t.Fatal(err)
	}
	return append(body, ed25519.Sign(key, body)...)
}

func configuredTestNativeCluster(t *testing.T, count int) (*s2cTestNetwork, map[uint32]ed25519.PrivateKey, *ConfiguredTrust) {
	t.Helper()
	f, keys, desired := configuredTestFixture(t, count)
	return configuredTestNativeFixture(t, f, keys, desired), keys, desired
}

func configuredTestNativeFixture(t *testing.T, f *s2cTestFixture, keys map[uint32]ed25519.PrivateKey, desired *ConfiguredTrust) *s2cTestNetwork {
	t.Helper()
	n := &s2cTestNetwork{t: t, fixture: f, configs: make(map[uint32]s2cParticipantConfig), nodes: make(map[uint32]*s2cParticipant)}
	dir := t.TempDir()
	for _, member := range f.members {
		c := s2cTestConfig(t, f, member.ID, filepath.Join(dir, fmt.Sprint(member.ID)))
		c.configured = desired
		o, err := createS2CParticipant(c)
		if err != nil {
			t.Fatal("create configured native participant", err)
		}
		n.configs[member.ID], n.nodes[member.ID] = c, o
	}
	t.Cleanup(func() {
		for _, node := range n.nodes {
			if node != nil {
				_ = node.Close()
			}
		}
	})
	return n
}

func TestConfiguredTrustOperationClassAndCanonicalEncoding(t *testing.T) {
	f, _, desired := configuredTestFixture(t, 1)
	op, err := newConfiguredOperation(f.genesis.state.projection.cut, desired)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeConfiguredOperation(op)
	if err != nil || decoded.Digest() != desired.Digest() || op.actor != (Identity{}) {
		t.Fatal("configured operation did not retain its separate class", err)
	}
	if _, err := op.command(); err == nil {
		t.Fatal("configured intent became an ordinary management command")
	}
	ordinary := s1Operation(t, f.genesis.state.projection, testIdentity(), s1Changes(s1ReaderRole()))
	if _, err := decodeConfiguredOperation(ordinary); err == nil {
		t.Fatal("OIDC management authorization entered configured class")
	}
	for name, mutate := range map[string]func(*OperationIdentity){
		"OIDC actor":        func(op *OperationIdentity) { op.actor = testIdentity() },
		"machine actor":     func(op *OperationIdentity) { op.actor = Identity{Kind: MachinePrincipal, MachineName: "worker"} },
		"wrong digest":      func(op *OperationIdentity) { op.digest[0] ^= 1 },
		"reviewed revision": func(op *OperationIdentity) { op.reviewed.Sequence++ },
		"extra space":       func(op *OperationIdentity) { op.canonical += " "; op.digest = sha256.Sum256([]byte(op.canonical)) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := op
			mutate(&bad)
			if _, err := decodeConfiguredOperation(bad); err == nil {
				t.Fatal("invalid configured operation accepted")
			}
		})
	}
	for _, invalid := range []*ConfiguredTrust{nil, {}, {issuers: desired.issuers, digest: [32]byte{9}}} {
		if _, err := newConfiguredOperation(op.reviewed, invalid); err == nil {
			t.Fatal("invalid desired capability constructed configured operation")
		}
	}
}

func TestConfiguredTrustBodyFramingAndBounds(t *testing.T) {
	// The validated complete-frame ceiling must fit int even on a 32-bit Go
	// target; the encoder checks this ceiling before allocating its capacity.
	if s2cMaxHistoricalBytes > uint64(1<<31-1) {
		t.Fatal("historical frame ceiling exceeds the portable allocation bound")
	}
	f, keys, desired := configuredTestFixture(t, 1)
	h, op := configuredTestHeader(t, f, keys, desired)
	header, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	operation := op.Encode()
	var expected bytes.Buffer
	expected.WriteString(configuredTrustMagic)
	for _, record := range [][]byte{header, operation} {
		if err := binary.Write(&expected, binary.BigEndian, uint32(len(record))); err != nil {
			t.Fatal(err)
		}
		expected.Write(record)
	}
	// Derive the signed limit from the independent wire encoding, rather than
	// reproducing the encoder's allocation-size calculation.
	signedBytes := uint64(expected.Len()) + ed25519.SignatureSize
	for _, tc := range []struct {
		name                    string
		headerBytes, historical uint64
		accept                  bool
	}{
		{"exact signed frame", uint64(len(header)), signedBytes, true},
		{"one byte spare", uint64(len(header)), signedBytes + 1, true},
		{"signature reserve short by one", uint64(len(header)), signedBytes - 1, false},
		{"unsigned body only", uint64(len(header)), uint64(expected.Len()), false},
		{"header short by one", uint64(len(header)) - 1, signedBytes, false},
		{"invalid historical ceiling", uint64(len(header)), s2cMaxHistoricalBytes + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bounds := f.trust.bounds
			bounds.HeaderBytes, bounds.HistoricalBytes = tc.headerBytes, tc.historical
			body, err := configuredTrustBody(h, operation, bounds)
			if !tc.accept {
				if !errors.Is(err, errS2CHistorical) || body != nil {
					t.Fatal("invalid complete-frame reservation accepted", err)
				}
				return
			}
			if err != nil || !bytes.Equal(body, expected.Bytes()) || uint64(cap(body)) != signedBytes {
				t.Fatal("canonical body or signature capacity changed", err)
			}
			signed := append(body, ed25519.Sign(keys[1], body)...)
			if uint64(len(signed)) != signedBytes {
				t.Fatal("signed frame did not use its exact reservation")
			}
			if verified, err := verifyHistoricalH(f.trust, signed); err != nil || verified.digest() != h.Value {
				t.Fatal("equivalent signed frame lost native proof verification", err)
			}
		})
	}
}

func TestConfiguredTrustHistoricalProofRejectsMutations(t *testing.T) {
	f, keys, desired := configuredTestFixture(t, 3)
	h, op := configuredTestHeader(t, f, keys, desired)
	raw := configuredTestSeal(t, f, keys[1], h, op)
	verified, err := verifyHistoricalH(f.trust, raw)
	if err != nil || verified.handoff.authorization != nil || verified.handoff.configured == nil || verified.digest() != h.Value {
		t.Fatal("genuine native configuration proof refused or class mixed", err)
	}
	for name, mutate := range map[string]func(*configuredTrustHeader){
		"zero version":          func(h *configuredTrustHeader) { h.Version = 0 },
		"other version":         func(h *configuredTrustHeader) { h.Version++ },
		"scope":                 func(h *configuredTrustHeader) { h.Scope[0] ^= 1 },
		"zero origin":           func(h *configuredTrustHeader) { h.OriginID = 0 },
		"unenrolled origin":     func(h *configuredTrustHeader) { h.OriginID = 900 },
		"other enrolled origin": func(h *configuredTrustHeader) { h.OriginID = 2 },
		"origin digest":         func(h *configuredTrustHeader) { h.OriginDigest[0] ^= 1 },
		"incarnation":           func(h *configuredTrustHeader) { h.Incarnation = [16]byte{} },
		"serial":                func(h *configuredTrustHeader) { h.Serial = 0 },
		"namespace":             func(h *configuredTrustHeader) { h.ID.Namespace++ },
		"nonce":                 func(h *configuredTrustHeader) { h.ID.Nonce = [16]byte{} },
		"predecessor":           func(h *configuredTrustHeader) { h.Predecessor[0] ^= 1 },
		"desired":               func(h *configuredTrustHeader) { h.Desired[0] ^= 1 },
		"operation":             func(h *configuredTrustHeader) { h.Operation[0] ^= 1 },
		"value":                 func(h *configuredTrustHeader) { h.Value[0] ^= 1 },
		"missing endorsements":  func(h *configuredTrustHeader) { h.Endorsements = nil },
		"minority endorsements": func(h *configuredTrustHeader) { h.Endorsements = h.Endorsements[:1] },
		"slot":                  func(h *configuredTrustHeader) { h.Slot++ },
		"zero consume":          func(h *configuredTrustHeader) { h.ConsumeLow = time.Time{} },
		"inverted consume":      func(h *configuredTrustHeader) { h.ConsumeHigh = h.ConsumeLow.Add(-time.Nanosecond) },
		"expired consume":       func(h *configuredTrustHeader) { h.ConsumeHigh = h.Endorsements[0].ExpiresAt },
	} {
		t.Run(name, func(t *testing.T) {
			bad := h
			mutate(&bad)
			if _, err := verifyHistoricalH(f.trust, configuredTestSeal(t, f, keys[1], bad, op)); err == nil {
				t.Fatal("signed but inconsistent native configuration header accepted")
			}
		})
	}
	for _, n := range []int{0, len(configuredTrustMagic) + 3, len(raw) - 1} {
		if _, err := verifyHistoricalH(f.trust, raw[:n]); err == nil {
			t.Fatal("truncated configured proof accepted", n)
		}
	}
	for _, offset := range []int{0, len(configuredTrustMagic) - 1, len(raw) - 1} {
		bad := bytes.Clone(raw)
		bad[offset] ^= 1
		if _, err := verifyHistoricalH(f.trust, bad); err == nil {
			t.Fatal("tampered configured proof accepted", offset)
		}
	}
	if _, err := verifyHistoricalH(f.trust, configuredTestSeal(t, f, f.keys[1], h, op)); err == nil {
		t.Fatal("ordinary member voting key replaced enrolled configuration origin")
	}
	legacy, _ := authorityTestFixture(t, 3)
	if _, err := verifyHistoricalH(legacy.trust, raw); err == nil {
		t.Fatal("unenrolled historical profile accepted configuration proof")
	}
	// Dual authorization is not a way to turn an OIDC or machine credential into
	// this class, even with an otherwise valid configured authorization closure.
	mixed := *verified.handoff
	mixed.authorization = &s1VerifiedAuthorization{}
	if mixed.valid(f.genesis.state.projection.cut) || mixed.digest() != [32]byte{} {
		t.Fatal("mixed authorization classes accepted")
	}
}

func TestConfiguredTrustNativeQuorumAndIntactResume(t *testing.T) {
	for _, count := range []int{1, 3} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			n, keys, desired := configuredTestNativeCluster(t, count)
			h, op := configuredTestHeader(t, n.fixture, keys, desired)
			raw := configuredTestSeal(t, n.fixture, keys[1], h, op)
			o := n.nodes[1]
			original := o.replayState.projection.Image()
			floor := o.p.floor
			if _, err := o.persistSealedOriginH(raw); err == nil || o.p.floor != floor {
				t.Fatal("configuration proof bypassed native durable origin reservation", err)
			}
			o.gate.Lock()
			_, err := o.reserveOriginLocked(h.ID, op.digest, s2cCredit{})
			o.gate.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			digest, err := o.persistSealedOriginH(raw)
			if err != nil || digest != h.Value {
				t.Fatal("native configuration origin retention", err)
			}
			if o.replayState.slot != 0 || !reflect.DeepEqual(o.replayState.projection.Image(), original) {
				t.Fatal("origin retention materialized without choice")
			}
			quorum := []uint32{1}
			if count == 3 {
				quorum = append(quorum, 2)
			}
			n.selectValue(1, digest, quorum...)
			if o.replayState.slot != 0 {
				t.Fatal("promise majority became Apply authority")
			}
			for _, member := range quorum {
				n.send(s2cAccept, 1, member)
			}
			n.send(s2cAccepted, 1, 1)
			if count == 3 {
				if o.replayState.slot != 0 || o.chosen != nil {
					t.Fatal("one fixed member manufactured a native choice")
				}
				n.send(s2cAccepted, 2, 1)
			}
			if o.replayState.slot != 1 || o.replayState.projection.Image().ConfiguredTrustRevision != 1 || len(o.replayState.ledger) != 0 ||
				!reflect.DeepEqual(o.replayState.projection.Image().Principals, original.Principals) {
				t.Fatal("certified configuration changed business enrollment or failed to apply")
			}
			issuer, known := o.replayState.projection.snapshot.Issuer(desired.issuers[0].URL)
			if !known || !issuer.EnvOwned || issuer.ConfigRevision != 1 {
				t.Fatal("configuration addition missing after native quorum")
			}
			prefix, cut := o.replayState.prefix, o.replayState.projection.cut
			n.restart(1)
			if n.nodes[1].replayState.prefix != prefix || n.nodes[1].replayState.projection.cut != cut {
				t.Fatal("intact native replay changed configured transition")
			}
		})
	}
}

func TestConfiguredTrustAuthenticatedStaleReviewCannotApply(t *testing.T) {
	f, keys, desired := configuredTestFixture(t, 3)
	h, _ := configuredTestHeader(t, f, keys, desired)
	h.Reviewed.Sequence++
	op, err := newConfiguredOperation(h.Reviewed, desired)
	if err != nil {
		t.Fatal(err)
	}
	h.Operation = op.digest
	d, _ := f.trust.origin(h.OriginID)
	handoff := &S1Handoff{id: h.ID, operation: op, origin: h.OriginDigest, serial: h.Serial,
		configured: &authorityConfiguredAuthorization{desired: desired, predecessor: h.Predecessor, slot: h.Slot, capability: configuredTrustCapability(f.trust, d)}}
	h.Value = handoff.digest()
	h.Endorsements = configuredTestEndorsements(f, keys, configuredStatement(h), f.trust.majority())
	historical, err := verifyHistoricalH(f.trust, configuredTestSeal(t, f, keys[1], h, op))
	if err != nil {
		t.Fatal("authenticated historical review should remain recognizable", err)
	}
	o := s2cPreviewTestOwner(t, f, s2OwnerTestScope(f.genesis))
	state, receipt, err := o.ReadLocalCut()
	if err != nil {
		t.Fatal(err)
	}
	commit := s2cLogicalCommit(f.trust, state.slot+1, historical.digest())
	if _, err := s2cPreviewCandidate(f.trust, state, f.genesis.roots, o.scope, receipt, historical, commit); !errors.Is(err, ErrRevisionConflict) {
		t.Fatal("genuine signatures over another review bypassed exact-cut CAS", err)
	}
}

func TestConfiguredTrustExactPredecessorSurvivesSameCutNoop(t *testing.T) {
	f, keys, desired := configuredTestFixture(t, 3)
	h, op := configuredTestHeader(t, f, keys, desired)
	historical, err := verifyHistoricalH(f.trust, configuredTestSeal(t, f, keys[1], h, op))
	if err != nil {
		t.Fatal(err)
	}
	o := s2cPreviewTestOwner(t, f, s2OwnerTestScope(f.genesis))
	p, state := s2cPreviewTestCandidate(t, f, o, historical)
	if _, err := ApplyS1(state, S1CertifiedNext{handoff: historical.handoff}); err == nil {
		t.Fatal("preview or endorsements manufactured prefix certificate")
	}
	qc := s2cPreviewTestQC(t, f, p, historical, s2cBallot{1, 1}, 1, 2)
	result, err := ApplyS1(state, qc)
	if err != nil || result.State.projection.Image().ConfiguredTrustRevision != 1 {
		t.Fatal("genuine native QC could not apply configured preview", err)
	}
	// A separately certified NOOP changes the prefix while preserving the exact
	// reviewed semantic cut. The old configuration assertion must still fail.
	noop, initial := s2cPreviewTestCandidate(t, f, o, nil)
	noopQC := s2cPreviewTestQC(t, f, noop, nil, s2cBallot{2, 1}, 1, 2)
	plan, err := o.PrepareNext(noopQC)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.CommitPrepared(plan); err != nil {
		t.Fatal(err)
	}
	current, receipt, err := o.ReadLocalCut()
	if err != nil || current.projection.cut != initial.projection.cut || current.prefix == initial.prefix {
		t.Fatal("same-cut NOOP fixture", err)
	}
	commit := s2cLogicalCommit(f.trust, current.slot+1, historical.digest())
	if _, err := s2cPreviewCandidate(f.trust, current, f.genesis.roots, o.scope, receipt, historical, commit); !errors.Is(err, ErrRevisionConflict) {
		t.Fatal("old exact-cut attestation replayed at a new native predecessor", err)
	}
}

var configuredTestUTC = time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)

func TestConfiguredTrustRecoversAcceptedHistoricalIntentBeforeNewDesired(t *testing.T) {
	n, keys, desiredA := configuredTestNativeCluster(t, 3)
	owners := make(map[uint32]*authorityOriginOwner)
	for id := range n.nodes {
		owners[id] = configuredTestEndorser(t, n, keys, id)
	}
	statement := configuredOwnerStatement(n.nodes[1])
	valueA, err := owners[1].sealConfigured(t.Context(), statement, configuredOwnerVotes(t, owners, statement))
	if err != nil {
		t.Fatal(err)
	}
	// Phase one approved A, but only one fixed voter durably accepted it. Drop
	// every response and all other deliveries before an external restart.
	n.selectValue(1, valueA, 1, 2)
	n.send(s2cAccept, 1, 1)
	if n.nodes[1].accepted == nil || n.nodes[1].accepted.value != valueA || n.nodes[2].accepted != nil || n.nodes[3].accepted != nil || n.nodes[1].replayState.slot != 0 {
		t.Fatal("hidden one-voter ACCEPT fixture")
	}
	n.queue = nil
	issuerB := desiredA.issuers[0].clone()
	issuerB.ClientID += "-next"
	desiredB, err := NewConfiguredTrust([]Issuer{issuerB})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint32{1, 2, 3} {
		config := n.configs[id]
		config.configured = desiredB
		n.configs[id] = config
		n.restart(id)
		owners[id] = configuredTestEndorser(t, n, keys, id)
	}
	// Today's B is only a local desired input. Zero is a phase-one preference;
	// it cannot replace the authentic highest-accepted A carried by voter one.
	n.selectValue(3, [32]byte{}, 1, 3)
	if n.nodes[3].selected == nil || n.nodes[3].selected.value != valueA {
		t.Fatal("new desired intent vetoed or replaced highest-accepted historical A")
	}
	n.choose(3, 1, 3)
	configuredOwnerConverge(t, n, 3)
	for id, node := range n.nodes {
		if node.replayState.slot != 1 || node.replayState.projection.snapshot.configuredTrustDigest != fmt.Sprintf("%x", desiredA.Digest()) || node.configuredReadyLocked() {
			t.Fatal("historical A did not recover while new desired B stayed closed", id)
		}
		receiver := &authorityRenewalReceiver{kernel: node, clock: owners[id].network.timeOwner, workloads: [32]byte{9}}
		if _, err := receiver.challenge(); err == nil {
			t.Fatal("public renewal used recovered A despite local B", id)
		}
	}
	statement = configuredOwnerStatement(n.nodes[3])
	valueB, err := owners[3].sealConfigured(t.Context(), statement, configuredOwnerVotes(t, owners, statement))
	if err != nil || valueB == valueA {
		t.Fatal("new intent could not reconcile after historical recovery", err)
	}
	n.selectValue(3, valueB, 2, 3)
	n.choose(3, 2, 3)
	configuredOwnerConverge(t, n, 3)
	for id, node := range n.nodes {
		if node.replayState.slot != 2 || node.replayState.projection.Image().ConfiguredTrustRevision != 2 || !node.configuredReadyLocked() {
			t.Fatal("current B did not certify after recovered A", id)
		}
	}
	if n.fixture.genesis.state.slot != 0 || n.fixture.genesis.state.projection.Image().ConfiguredTrustRevision != 0 {
		t.Fatal("recovery rewrote historical genesis")
	}
}

func TestConfiguredTrustRecoveryCanMakeInitiallyInvalidDesiredLegal(t *testing.T) {
	image := testImage()
	image.Issuers[0].EnvOwned = true
	image.Issuers[0].HumanSubjectNamespaceQualified = true
	issuerB := image.Issuers[0].clone()
	issuerB.URL, issuerB.EnvOwned = "https://management.example", false
	image.Issuers = append(image.Issuers, issuerB)
	subjectB := Identity{Kind: OIDCPrincipal, Issuer: issuerB.URL, Subject: "enrolled-member"}
	image.Principals = append(image.Principals, Principal{Identity: subjectB, State: Active, HumanIssuerConfigRevision: 1})
	desiredA := configuredIntent(t, image.Issuers[0])
	image.ConfiguredTrustRevision, image.ConfiguredTrustDigest = 1, fmt.Sprintf("%x", desiredA.Digest())
	f, keys := authorityTestFixtureState(t, s2cTestClusterState(t, 3, s1Fixture(t, image)))
	configuredTestEnroll(t, f)
	n := configuredTestNativeFixture(t, f, keys, desiredA)
	desiredB := configuredIntent(t)
	if _, err := assessConfiguredTrust(n.nodes[1].replayState.projection, desiredB); !errors.Is(err, ErrLastAdministrator) {
		t.Fatal("withdrawal should initially lose the only usable administrator", err)
	}
	op := s1Operation(t, f.genesis.state.projection, testIdentity(), s1Changes(Change{Kind: PutAssignment, Identity: &subjectB, RoleID: "security_admin"}))
	assessment, err := AssessS1(f.genesis.state.projection, op)
	if err != nil || !assessment.NeedsPurpose {
		t.Fatal("administrator grant did not require operation-specific purpose", err)
	}
	// This is the existing genuine historical codec fixture: the original
	// administrator approves one exact post-review operation while A is current.
	h, _ := authorityTestHeader(t, f)
	h.OperationDigest = op.digest
	low, _ := h.Time.utcTimes()
	event := *h.Credential.Token
	event.Mode, event.Profile, event.Nonce = "code", "oidc-id", [32]byte{10}
	event.IssuedAt, event.AuthTime = authorityNumericTime(low.Add(-2*time.Second)), authorityNumericTime(low.Add(-2*time.Second))
	event.Code = CodeAuthenticationEvidence{Flow: "operation", AuthorizationID: [32]byte{3}, Transaction: [32]byte{4}, Exchange: [32]byte{5}, Nonce: event.Nonce, PKCE: [32]byte{6},
		CreatedAt: low.Add(-3 * time.Second), ConsumedAt: low.Add(-time.Second), ExpiresAt: low.Add(time.Minute)}
	purpose := authorityPurposeClaim{Kind: "full-s1-operation", Binding: authorityPurposeBinding{h.ID, op.digest, op.reviewed, op.actor, 1, 1}, Authorization: event.Code.AuthorizationID,
		Challenge: [32]byte{7}, ReviewAt: low.Add(-5 * time.Second), NotBefore: low.Add(-4 * time.Second), ApprovedAt: low.Add(-time.Second), ExpiresAt: low.Add(time.Minute), Event: event,
		Origin: h.OriginDigest, Serial: h.Serial}
	purpose.EventCommitment, err = event.Commitment()
	if err != nil {
		t.Fatal(err)
	}
	h.Purpose, h.PurposeDeadline = &purpose, purpose.ExpiresAt
	raw := authorityTestSealHeader(t, f, keys[1], h, op)
	historical, err := verifyHistoricalH(f.trust, raw)
	if err != nil || historical.handoff.configured != nil || historical.handoff.authorization == nil {
		t.Fatal("ordinary historical purpose proof was not genuine", err)
	}
	k := n.nodes[1]
	k.gate.Lock()
	_, err = k.reserveOriginLocked(h.ID, op.digest, s2cCredit{})
	k.gate.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	managementValue, err := k.persistSealedOriginH(raw)
	if err != nil {
		t.Fatal(err)
	}
	n.selectValue(1, managementValue, 1, 2)
	n.send(s2cAccept, 1, 1)
	if n.nodes[1].accepted == nil || n.nodes[1].replayState.slot != 0 || n.nodes[2].accepted != nil || n.nodes[3].accepted != nil {
		t.Fatal("ordinary management hidden ACCEPT fixture")
	}
	n.queue = nil
	owners := make(map[uint32]*authorityOriginOwner)
	for _, id := range []uint32{1, 2, 3} {
		config := n.configs[id]
		config.configured = desiredB
		n.configs[id] = config
		n.restart(id)
		owners[id] = configuredTestEndorser(t, n, keys, id)
		if _, err = assessConfiguredTrust(n.nodes[id].replayState.projection, desiredB); !errors.Is(err, ErrLastAdministrator) {
			t.Fatal("restart accidentally repaired initially invalid desired intent", id, err)
		}
	}
	n.selectValue(3, [32]byte{}, 1, 3)
	if n.nodes[3].selected == nil || n.nodes[3].selected.value != managementValue {
		t.Fatal("invalid current desired plan prevented ordinary highest-accepted recovery")
	}
	n.choose(3, 1, 3)
	configuredOwnerConverge(t, n, 3)
	for id, node := range n.nodes {
		access, active := node.replayState.projection.snapshot.AccessFor(subjectB)
		if !active || !access.AllowsGlobal(SecurityManage) || !node.replayState.projection.snapshot.HumanIdentity(subjectB) || node.configuredReadyLocked() {
			t.Fatal("recovered ordinary management did not make B a qualified administrator while public stayed closed", id)
		}
		if _, err := assessConfiguredTrust(node.replayState.projection, desiredB); err != nil {
			t.Fatal("retained management outcome failed to make desired withdrawal legal", id, err)
		}
	}
	statement := configuredOwnerStatement(n.nodes[3])
	withdrawal, err := owners[3].sealConfigured(t.Context(), statement, configuredOwnerVotes(t, owners, statement))
	if err != nil {
		t.Fatal("newly legal desired withdrawal could not certify", err)
	}
	n.selectValue(3, withdrawal, 2, 3)
	n.choose(3, 2, 3)
	configuredOwnerConverge(t, n, 3)
	for id, node := range n.nodes {
		issuerA, _ := node.replayState.projection.snapshot.Issuer(testIdentity().Issuer)
		if !node.configuredReadyLocked() || issuerA.Enabled || !issuerA.Deleted || node.replayState.projection.Image().ConfiguredTrustRevision != 2 {
			t.Fatal("recovered administrator did not permit certified trust withdrawal", id)
		}
		status, outcome, _, err := node.LookupOriginal(h.ID)
		if err != nil || status != S1Known || outcome == nil || outcome.Disposition() != S1Applied || outcome.Commit().Slot != 1 {
			t.Fatal("ordinary original outcome was lost during configured reconciliation", id, err)
		}
	}
}

func TestConfiguredTrustImageReserveProtectsWithdrawalCapacity(t *testing.T) {
	for _, withdraw := range []bool{false, true} {
		t.Run(fmt.Sprintf("withdraw-%t", withdraw), func(t *testing.T) {
			projection := configuredProjectionFixture(t, true)
			var desired *ConfiguredTrust
			if withdraw {
				desired = configuredIntent(t)
			} else {
				issuer := projection.Image().Issuers[0].clone()
				issuer.URL = "https://reserve-new.example"
				desired = configuredIntent(t, projection.Image().Issuers[0], issuer)
			}
			planned, err := assessConfiguredTrust(projection, desired)
			if err != nil {
				t.Fatal(err)
			}
			// Both transitions fit the full image ceiling, but neither can fit
			// the deliberately exhausted ordinary (one byte) portion of it.
			capacity := S1Capacity{LedgerEntries: 100, RestrictiveEntries: 10, ImageBytes: uint32(max(len(projection.snapshot.image), len(planned.snapshot.image)))}
			capacity.RestrictiveImageBytes = capacity.ImageBytes - 1
			initial, err := NewS1ApplyState(projection, [32]byte{5}, capacity, S1Retention{})
			if err != nil {
				t.Fatal(err)
			}
			f, keys := authorityTestFixtureState(t, s2cTestClusterState(t, 3, initial))
			configuredTestEnroll(t, f)
			h, op := configuredTestHeader(t, f, keys, desired)
			historical, err := verifyHistoricalH(f.trust, configuredTestSeal(t, f, keys[1], h, op))
			if err != nil {
				t.Fatal(err)
			}
			owner := s2cPreviewTestOwner(t, f, s2OwnerTestScope(f.genesis))
			state, receipt, err := owner.ReadLocalCut()
			if err != nil {
				t.Fatal(err)
			}
			commit := s2cLogicalCommit(f.trust, state.slot+1, historical.digest())
			preview, err := s2cPreviewCandidate(f.trust, state, f.genesis.roots, owner.scope, receipt, historical, commit)
			if !withdraw {
				if !errors.Is(err, ErrControlReserve) || preview != nil || owner.state.slot != 0 {
					t.Fatal("issuer addition with no principals consumed revocation reserve", err)
				}
				return
			}
			if err != nil || preview == nil {
				t.Fatal("pure withdrawal could not use reserved image capacity", err)
			}
			qc := s2cPreviewTestQC(t, f, preview, historical, s2cBallot{1, 1}, 1, 2)
			plan, err := owner.PrepareNext(qc)
			if err != nil {
				t.Fatal("genuine certified withdrawal failed inside reserve", err)
			}
			if _, err = owner.CommitPrepared(plan); err != nil {
				t.Fatal("native withdrawal could not materialize from revocation reserve", err)
			}
			issuerA, _ := owner.state.projection.snapshot.Issuer(testIdentity().Issuer)
			if owner.state.slot != 1 || issuerA.Enabled || !issuerA.Deleted {
				t.Fatal("withdrawal in reserve did not preserve its restrictive effect")
			}
		})
	}
}
