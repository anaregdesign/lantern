package security

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Automatic workers are deliberately blocked in these fixtures. A range RPC
// can compete with an in-flight CHOSEN for the same finite per-peer ingress
// credit. Retry only needed authenticated ranges in a bounded window, then
// require every live owner to have the exact prefix/capsule before fresh renewal.
// Reading fixture receipts selects a source; only the real CatchUp path installs
// its signed CHOSEN history, never these test observations.
func authorityTestConverge(t *testing.T, owners map[uint32]*authorityOriginOwner) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var last error
	for {
		receipts := make(map[uint32]s2LocalReceipt)
		var source uint32
		for id, o := range owners {
			if o.network.closed.Load() {
				continue
			}
			_, receipt, err := o.network.kernel.ReadLocalCut()
			if err != nil {
				t.Fatal("read convergence receipt", id, err)
			}
			receipts[id] = receipt
			if source == 0 || receipt.ControlSlot > receipts[source].ControlSlot {
				source = id
			}
		}
		complete := true
		for id, receipt := range receipts {
			if receipt.ControlSlot == receipts[source].ControlSlot {
				// B store identity and local chain are intentionally per-member.
				if receipt.ControlPrefix != receipts[source].ControlPrefix || receipt.CapsuleDigest != receipts[source].CapsuleDigest {
					t.Fatal("converged slot has different prefix/capsule", id, source)
				}
				continue
			}
			complete = false
			if _, err := owners[id].network.CatchUp(ctx, source); err != nil {
				last = err
			}
		}
		if complete {
			return
		}
		if !s3aPause(ctx, 20*time.Millisecond) {
			t.Fatal("authenticated convergence deadline", ctx.Err(), last)
		}
	}
}

func authorityTestComposite(t *testing.T) (*s3aTestNetwork, map[uint32]*authorityOriginOwner, map[uint32]*atomic.Uint64) {
	return authorityTestCompositeOrigins(t, 3)
}

func authorityTestCompositeOrigins(t *testing.T, originCount int) (*s3aTestNetwork, map[uint32]*authorityOriginOwner, map[uint32]*atomic.Uint64) {
	t.Helper()
	n := s3aTestCluster(t, nil)
	f := s2cTestCluster(t, 3)
	f.origins = f.origins[:originCount]
	f, originKeys := authorityTestFixtureState(t, f)
	n.f = f
	n.manifest.Profile.ProtocolScope = f.trust.scope
	raw := n.sign(n.manifest)
	owners, ticks := make(map[uint32]*authorityOriginOwner), make(map[uint32]*atomic.Uint64)
	for id, c := range n.configs {
		c.Participant.Trust = f.trust
		if int(id) > originCount {
			c.Participant.OwnedOrigin = 0
		}
		c.Membership.Profile, c.Manifest = n.manifest.Profile, raw
		clock, counter := fakeAuthorityTimeOwnerAt(t, time.Unix(0, n.clock.Load()).UTC())
		ticks[id] = counter
		if err := bindAuthorityNetworkTime(&c, clock); err != nil {
			t.Fatal(err)
		}
		c.hooks = &s3aHooks{beforeRenewal: func(ctx context.Context) { <-ctx.Done() }}
		var key ed25519.PrivateKey
		if c.Participant.OwnedOrigin != 0 {
			keyPath := filepath.Join(filepath.Dir(c.Identity.VotingKey), "origin.key")
			if err := os.WriteFile(keyPath, originKeys[id], 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			key, err = loadAuthorityOriginKey(c, keyPath)
			if err != nil {
				t.Fatal(err)
			}
		}
		n.configs[id] = c
		owner, err := createS3AOwner(c)
		if err != nil {
			t.Fatal(err)
		}
		n.nodes[id] = owner
		origin, err := attachAuthorityOrigin(owner, key, "https://admin.example")
		if err != nil {
			t.Fatal(err)
		}
		owners[id] = origin
	}
	for id, owner := range n.nodes {
		if err := owner.Start(n.listeners[id]); err != nil {
			t.Fatal(err)
		}
	}
	return n, owners, ticks
}

// Explicit factual unit fixture. It does not qualify the actual provider-to-H
// path; those gates use real signed tokens/Code/cookies and native time.
type authorityFakeCredentialProducer struct{ expiry time.Duration }

func (producer authorityFakeCredentialProducer) VerifyCurrentCredential(_ context.Context, view CurrentCredentialView) (CurrentCredentialFacts, error) {
	low, high, err := view.TimeBounds()
	if err != nil {
		return CurrentCredentialFacts{}, err
	}
	actor := testIdentity()
	i, known := view.Snapshot().Issuer(actor.Issuer)
	if !known {
		return CurrentCredentialFacts{}, ErrPermissionDenied
	}
	lifetime := producer.expiry
	if lifetime == 0 {
		lifetime = time.Hour
	}
	e := TokenAuthenticationEvidence{Version: 1, Mode: "access", Profile: "rfc9068", Policy: "lantern-oidc-v1", Identity: actor,
		Credential: [32]byte{1}, Key: [32]byte{2}, Configuration: authorityIssuerCommitment(i, view.Cut().Generation), AudienceClient: [32]byte{4}, Algorithm: "EdDSA", KeyID: "unit-facts-only",
		Generation: view.Cut().Generation, ConfigRevision: i.ConfigRevision, IssuedAt: authorityNumericTime(low.Add(-time.Minute)), ExpiresAt: authorityNumericTime(high.Add(lifetime))}
	return CurrentCredentialFacts{Token: &e}, nil
}

func authorityTestReceiver(t *testing.T, n *s2cTestNetwork) (*authorityRenewalReceiver, *atomic.Uint64) {
	t.Helper()
	clock, ticks := fakeAuthorityTimeOwnerAt(t, time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC))
	r := &authorityRenewalReceiver{kernel: n.nodes[1], clock: clock, workloads: [32]byte{9}}
	request, err := r.challenge()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint32{1, 2} {
		vote, err := n.nodes[id].signAuthorityRenewal(request, r.workloads, func() error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		if err = r.receive(request, vote); err != nil {
			t.Fatal(err)
		}
	}
	return r, ticks
}

func authorityTestFixture(t *testing.T, count int) (*s2cTestFixture, map[uint32]ed25519.PrivateKey) {
	t.Helper()
	return authorityTestFixtureState(t, s2cTestCluster(t, count))
}

func authorityTestFixtureState(t *testing.T, f *s2cTestFixture) (*s2cTestFixture, map[uint32]ed25519.PrivateKey) {
	t.Helper()
	keys := make(map[uint32]ed25519.PrivateKey)
	for i := range f.origins {
		d := &f.origins[i]
		seed := sha256.Sum256([]byte(fmt.Sprintf("test-only-distinct-origin-%d", d.ID)))
		key := ed25519.NewKeyFromSeed(seed[:])
		keys[d.ID] = key
		copy(d.PublicKey[:], key.Public().(ed25519.PublicKey))
		d.Profile = authorityAdmissionProfile()
		d.Namespace = uint64(d.ID) + f.genesis.state.retiredThrough + 1
	}
	var err error
	f.trust, err = newS2CTrust(s2cBootstrap{f.genesis, f.members, f.origins, s2cTestBounds()})
	if err != nil {
		t.Fatal(err)
	}
	return f, keys
}

// Codec fixtures exercise representation/verification, not actual credential
// producers. The composed native qualification uses the production issuer.
func authorityTestHeader(t *testing.T, f *s2cTestFixture) (authorityHistoricalHeader, OperationIdentity) {
	t.Helper()
	op := s1Operation(t, f.genesis.state.projection, testIdentity(), s1Changes(s1ReaderRole()))
	d, _ := f.trust.origin(1)
	now := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	h := authorityHistoricalHeader{Version: authorityAdmissionVersion, Scope: f.trust.scope, OriginID: 1, OriginDigest: f.trust.originDigests[1], Incarnation: d.Incarnation,
		ID: FullChangeID{S1Version, op.reviewed.Domain, op.reviewed.Cohort, d.Namespace, [16]byte{1}}, Serial: 1, OperationDigest: op.digest,
		Authentication: Authentication{Provenance: RFC9068Bearer, Class: EndUser, IssuerConfigRevision: 1}, Lineage: 1, Workloads: [32]byte{9}}
	h.Time = authorityConsumeTime{Profile: sha256.Sum256([]byte(authorityTimeProfileDescription)), HostBoot: [32]byte{1}, Process: [16]byte{2}, Counter: 2_000_000, StartCounter: 1_000_000, SourceSequence: 1,
		UTCLow: uint64(now.UnixNano()), UTCHigh: uint64(now.Add(time.Millisecond).UnixNano())}
	_, h.ConsumeUpper = h.Time.utcTimes()
	e := TokenAuthenticationEvidence{Version: 1, Mode: "access", Profile: "rfc9068", Policy: "lantern-oidc-v1", Identity: op.actor,
		Credential: [32]byte{1}, Key: [32]byte{2}, Configuration: [32]byte{3}, AudienceClient: [32]byte{4}, Algorithm: "EdDSA", KeyID: "codec-fixture",
		Generation: op.reviewed.Generation, ConfigRevision: 1, IssuedAt: authorityNumericTime(now.Add(-time.Minute)), ExpiresAt: authorityNumericTime(now.Add(time.Hour))}
	commitment, err := e.Commitment()
	if err != nil {
		t.Fatal(err)
	}
	h.Credential = authorityCredentialClaim{Kind: "access", Token: &e, Verification: commitment, Enrollment: [32]byte{5}, HumanNamespace: [32]byte{6}, DerivedStart: e.IssuedAt, AdmissionDeadline: now.Add(10 * time.Second)}
	h.CredentialDeadline = h.Credential.AdmissionDeadline
	capsule, _, err := s2EncodeCapsule(f.genesis.state, f.genesis.roots, f.trust.bounds.Capsule)
	if err != nil {
		t.Fatal(err)
	}
	s := authorityRenewalStatement{Version: 1, Scope: f.trust.scope, Members: f.trust.memberSet, Workloads: h.Workloads, TimeProfile: h.Time.Profile, Receiver: 1, Boot: h.Time.Process,
		Challenge: [32]byte{3}, Slot: 0, Prefix: f.genesis.state.prefix, Capsule: s2LocalCapsuleDigest(capsule), Cut: op.reviewed, Lifetime: authorityRenewalLifetime}
	requestRaw, err := signAuthorityRenewalRequest(s, f.trust, h.Workloads, f.keys[1])
	if err != nil {
		t.Fatal(err)
	}
	request, err := parseAuthorityRenewalRequest(requestRaw, f.trust, h.Workloads)
	if err != nil {
		t.Fatal(err)
	}
	var votes []authorityRenewalVote
	for _, m := range f.members {
		v := authorityRenewalVote{member: m.ID}
		copy(v.signature[:], ed25519.Sign(f.keys[m.ID], authorityRenewalVoteBytes(request.canonical, m.ID)))
		votes = append(votes, v)
	}
	certificate, err := encodeAuthorityRenewalCertificate(requestRaw, votes, f.trust, h.Workloads)
	if err != nil {
		t.Fatal(err)
	}
	h.Renewal = []byte(certificate.raw)
	return h, op
}

func authorityTestSealHeader(t *testing.T, f *s2cTestFixture, key ed25519.PrivateKey, h authorityHistoricalHeader, op OperationIdentity) []byte {
	t.Helper()
	h.CredentialEvidence = authorityCredentialEvidence(h)
	if h.Purpose != nil {
		h.PurposeEvidence = s2cHash("current-purpose-evidence-v2", h.Purpose)
		h.PurposeBinding = s1PurposeBinding(h.ID, op)
	}
	a := &s1VerifiedAuthorization{h.Authentication, h.Lineage, h.CredentialEvidence, h.PurposeEvidence, h.PurposeBinding, h.ConsumeUpper, h.CredentialDeadline, h.PurposeDeadline}
	h.Value = (&S1Handoff{h.ID, op, h.OriginDigest, h.Serial, a}).digest()
	body, err := authorityHistoricalBody(h, op.Encode(), f.trust.bounds)
	if err != nil {
		t.Fatal(err)
	}
	return append(body, ed25519.Sign(key, body)...)
}

func authorityTestNativeCluster(t *testing.T, count int) (*s2cTestNetwork, map[uint32]ed25519.PrivateKey) {
	t.Helper()
	f, keys := authorityTestFixture(t, count)
	n := &s2cTestNetwork{t: t, fixture: f, configs: make(map[uint32]s2cParticipantConfig), nodes: make(map[uint32]*s2cParticipant)}
	dir := t.TempDir()
	for _, m := range f.members {
		c := s2cTestConfig(t, f, m.ID, filepath.Join(dir, fmt.Sprint(m.ID)))
		o, err := createS2CParticipant(c)
		if err != nil {
			t.Fatal(err)
		}
		n.configs[m.ID], n.nodes[m.ID] = c, o
	}
	t.Cleanup(func() {
		for _, o := range n.nodes {
			_ = o.Close()
		}
	})
	return n, keys
}

// Shared current-authority fixture constructors; never production qualification.
func testAuthorityRenewalRequest(t *testing.T, n *s2cTestNetwork, id uint32, workloads [32]byte) []byte {
	t.Helper()
	s, r, err := n.nodes[id].ReadLocalCut()
	if err != nil {
		t.Fatal(err)
	}
	statement := authorityRenewalStatement{Version: 1, Scope: n.fixture.trust.scope, Members: n.fixture.trust.memberSet, Workloads: workloads,
		TimeProfile: sha256.Sum256([]byte(authorityTimeProfileDescription)), Receiver: id, Boot: [16]byte{1}, Challenge: [32]byte{2},
		Slot: s.slot, Prefix: s.prefix, Capsule: r.CapsuleDigest, Cut: s.projection.cut, Lifetime: authorityRenewalLifetime}
	raw, err := signAuthorityRenewalRequest(statement, n.fixture.trust, workloads, n.fixture.keys[id])
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
