package security

import (
	"crypto/ed25519"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The independent bootstrap is test input, never reconstructed from a peer's
// journal header, capsule, or claim. Each participant has a distinct key.
// Helpers here are shared by trust, evidence, preview, and native gate tests.
type s2cTestFixture struct {
	trust   *s2cTrust
	keys    map[uint32]ed25519.PrivateKey
	genesis *s2TrustedGenesis
	members []s2cMember
	origins []s2cOriginDescriptor
}

func s2cTestConfig(t *testing.T, f *s2cTestFixture, member uint32, dir string) s2cParticipantConfig {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	b := s2OwnerTestScope(f.genesis)
	b.StoreIdentity, b.JournalEpoch = [32]byte{0xb0, byte(member)}, [16]byte{0xb1, byte(member)}
	b.Storage.JournalBytes, b.Storage.ReplayRecords = 16<<20, 4096
	return s2cParticipantConfig{
		Trust: f.trust, Member: member, Incarnation: [16]byte{0x49, byte(member)}, Key: f.keys[member],
		PPath: filepath.Join(dir, "protocol.wal"), BPath: filepath.Join(dir, "materialized.wal"),
		PIdentity: [32]byte{0xa0, byte(member)}, PEpoch: [16]byte{0xa1, byte(member)},
		PPolicy: s2cJournalPolicy{Bytes: 16 << 20, Records: 4096}, BScope: b,
		OwnedOrigin:  member,
		PendingBytes: 8 << 20, PendingCount: 128, OutboxBytes: 32 << 20,
	}
}

// The scheduler transports copies of encoded messages only. It neither signs
// votes nor accesses accepted/chosen fields to manufacture protocol progress.
type s2cTestEnvelope struct {
	from, to uint32
	raw      []byte
}

func (e s2cTestEnvelope) kind() byte {
	if len(e.raw) <= len(s2cMessageMagic) {
		return 0
	}
	return e.raw[len(s2cMessageMagic)]
}

type s2cTestNetwork struct {
	t       *testing.T
	fixture *s2cTestFixture
	configs map[uint32]s2cParticipantConfig
	nodes   map[uint32]*s2cParticipant
	queue   []s2cTestEnvelope
	steps   int
}

func s2cTestNativeCluster(t *testing.T, n int, configure func(uint32, *s2cParticipantConfig)) *s2cTestNetwork {
	t.Helper()
	f := s2cTestCluster(t, n)
	net := &s2cTestNetwork{t: t, fixture: f, configs: make(map[uint32]s2cParticipantConfig), nodes: make(map[uint32]*s2cParticipant)}
	dir := t.TempDir()
	for _, member := range f.members {
		c := s2cTestConfig(t, f, member.ID, filepath.Join(dir, fmt.Sprintf("member-%d", member.ID)))
		if configure != nil {
			configure(member.ID, &c)
		}
		o, err := createS2CParticipant(c)
		if err != nil {
			t.Fatal("create native member", member.ID, err)
		}
		net.configs[member.ID], net.nodes[member.ID] = c, o
	}
	t.Cleanup(func() {
		for _, o := range net.nodes {
			if o != nil {
				_ = o.Close()
			}
		}
	})
	return net
}

func (n *s2cTestNetwork) enqueue(from uint32, out []s2cOutbox) {
	n.t.Helper()
	for _, message := range out {
		if _, ok := n.configs[message.To]; !ok || len(message.Bytes) == 0 {
			n.t.Fatal("invalid native outbox", from, message.To)
		}
		n.queue = append(n.queue, s2cTestEnvelope{from, message.To, append([]byte(nil), message.Bytes...)})
	}
}

func (n *s2cTestNetwork) begin(member uint32, candidate [32]byte) {
	n.t.Helper()
	out, err := n.nodes[member].Begin(candidate)
	if err != nil {
		n.t.Fatal("begin", member, err)
	}
	n.enqueue(member, out)
}

func (n *s2cTestNetwork) take(kind byte, from, to uint32) s2cTestEnvelope {
	n.t.Helper()
	for i, e := range n.queue {
		if e.kind() == kind && e.from == from && e.to == to {
			n.queue = append(n.queue[:i], n.queue[i+1:]...)
			return e
		}
	}
	n.t.Fatalf("missing encoded message kind=%d from=%d to=%d; queue=%v", kind, from, to, n.summary())
	return s2cTestEnvelope{}
}

func (n *s2cTestNetwork) summary() []string {
	var rows []string
	for _, e := range n.queue {
		rows = append(rows, fmt.Sprintf("%d:%d->%d", e.kind(), e.from, e.to))
	}
	return rows
}

func (n *s2cTestNetwork) deliver(e s2cTestEnvelope) error {
	n.t.Helper()
	o := n.nodes[e.to]
	if o == nil {
		n.t.Fatal("delivery to stopped member", e.to)
	}
	n.steps++
	out, err := o.Receive(append([]byte(nil), e.raw...))
	if err == nil {
		n.enqueue(e.to, out)
	}
	return err
}

func (n *s2cTestNetwork) send(kind byte, from, to uint32) {
	n.t.Helper()
	if err := n.deliver(n.take(kind, from, to)); err != nil {
		n.t.Fatal("encoded delivery", kind, from, to, err)
	}
}

func (n *s2cTestNetwork) selectValue(proposer uint32, candidate [32]byte, quorum ...uint32) {
	n.t.Helper()
	n.begin(proposer, candidate)
	for _, member := range quorum {
		n.send(s2cPrepare, proposer, member)
	}
	for i := len(quorum) - 1; i >= 0; i-- {
		n.send(s2cPromise, quorum[i], proposer)
	}
}

func (n *s2cTestNetwork) choose(proposer uint32, quorum ...uint32) {
	n.t.Helper()
	for _, member := range quorum {
		n.send(s2cAccept, proposer, member)
	}
	for i := len(quorum) - 1; i >= 0; i-- {
		n.send(s2cAccepted, quorum[i], proposer)
	}
}

func (n *s2cTestNetwork) stop(member uint32) {
	n.t.Helper()
	if o := n.nodes[member]; o != nil {
		if err := o.Close(); err != nil {
			n.t.Fatal("close member", member, err)
		}
	}
	n.nodes[member] = nil
}

func (n *s2cTestNetwork) restart(member uint32) {
	n.t.Helper()
	p, b, err := n.nodes[member].Floors()
	if err != nil {
		n.t.Fatal("retain independent floors", member, err)
	}
	n.stop(member)
	// No pointer into the old owner, decoded message, selected proof, or
	// retained certificate is an input to Resume.
	o, err := resumeS2CParticipant(n.configs[member], p, b)
	if err != nil {
		n.t.Fatal("cold member restart", member, err)
	}
	n.nodes[member] = o
}

func s2cTestCluster(t *testing.T, n int) *s2cTestFixture {
	t.Helper()
	return s2cTestClusterState(t, n, s1Fixture(t, s1Image()))
}

func s2cTestClusterState(t *testing.T, n int, initial *S1ApplyState) *s2cTestFixture {
	t.Helper()
	f := &s2cTestFixture{keys: make(map[uint32]ed25519.PrivateKey, n)}
	for i := 1; i <= n; i++ {
		id := uint32(i)
		seed := [ed25519.SeedSize]byte{0x53, 0x32, 0x43, byte(i)}
		key := ed25519.NewKeyFromSeed(seed[:])
		public := [ed25519.PublicKeySize]byte(key.Public().(ed25519.PublicKey))
		f.keys[id] = key
		f.members = append(f.members, s2cMember{ID: id, PublicKey: public, Proposer: true})
		f.origins = append(f.origins, s2cOriginDescriptor{ID: id, Member: id, PublicKey: public, Incarnation: [16]byte{0x49, byte(i)}, Profile: s2cTestProfile()})
	}
	membership, err := s2cMemberSetDigest(f.members)
	if err != nil {
		t.Fatal("independent members", err)
	}
	retention := S1Retention{}
	if initial.retiredThrough != 0 {
		retention = S1Retention{initial.retiredThrough, initial.projection.cut.Domain, initial.projection.cut.Cohort, [32]byte{0x52}}
	}
	state, err := NewS1ApplyState(initial.projection, membership, initial.configuration.Capacity, retention)
	if err != nil {
		t.Fatal("independent genesis", err)
	}
	f.genesis = s2TestGenesis(state)
	f.genesis.roots.Retirement = retention.checkpoint
	f.trust, err = newS2CTrust(s2cBootstrap{Genesis: f.genesis, Members: f.members, Origins: f.origins, Bounds: s2cTestBounds()})
	if err != nil {
		t.Fatal("independent trust", err)
	}
	return f
}
