package security

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/mutationlog"
)

func TestS2CParticipantIndependentConfiguration(t *testing.T) {
	f := s2cTestCluster(t, 3)
	cases := map[string]func(*s2cParticipantConfig){
		"member":        func(c *s2cParticipantConfig) { c.Member = 9 },
		"key":           func(c *s2cParticipantConfig) { c.Key = f.keys[2] },
		"incarnation":   func(c *s2cParticipantConfig) { c.Incarnation = [16]byte{} },
		"epoch":         func(c *s2cParticipantConfig) { c.PEpoch = [16]byte{} },
		"identity":      func(c *s2cParticipantConfig) { c.PIdentity = [32]byte{} },
		"origin_owner":  func(c *s2cParticipantConfig) { c.OwnedOrigin = 2 },
		"relative":      func(c *s2cParticipantConfig) { c.PPath = "protocol.wal" },
		"same_path":     func(c *s2cParticipantConfig) { c.BPath = c.PPath },
		"sidecar":       func(c *s2cParticipantConfig) { c.BPath = c.PPath + ".tip" },
		"pending_bytes": func(c *s2cParticipantConfig) { c.PendingBytes = c.PPolicy.Bytes + 1 },
		"pending_count": func(c *s2cParticipantConfig) { c.PendingCount = 0 },
		"outbox":        func(c *s2cParticipantConfig) { c.OutboxBytes = 0 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			c := s2cTestConfig(t, f, 1, t.TempDir())
			change(&c)
			if o, e := createS2CParticipant(c); e == nil {
				_ = o.Close()
				t.Fatal("invalid local bootstrap accepted")
			}
		})
	}
	t.Run("existing_B_precedes_P_creation", func(t *testing.T) {
		c := s2cTestConfig(t, f, 1, t.TempDir())
		if e := os.WriteFile(c.BPath, []byte("existing"), 0o600); e != nil {
			t.Fatal(e)
		}
		if _, e := createS2CParticipant(c); !errors.Is(e, os.ErrExist) {
			t.Fatal(e)
		}
		if _, e := os.Stat(c.PPath); !errors.Is(e, os.ErrNotExist) {
			t.Fatal("created P despite existing B", e)
		}
	})
	t.Run("hardlink_alias", func(t *testing.T) {
		c := s2cTestConfig(t, f, 1, t.TempDir())
		if e := os.WriteFile(c.PPath, []byte("same inode"), 0o600); e != nil {
			t.Fatal(e)
		}
		if e := os.Link(c.PPath, c.BPath); e != nil {
			t.Fatal(e)
		}
		if _, e := createS2CParticipant(c); e == nil {
			t.Fatal("hardlink family alias")
		}
	})
	t.Run("symlink_alias", func(t *testing.T) {
		c := s2cTestConfig(t, f, 1, t.TempDir())
		target := filepath.Join(filepath.Dir(c.PPath), "target")
		if e := os.WriteFile(target, nil, 0o600); e != nil {
			t.Fatal(e)
		}
		if e := os.Symlink(target, c.PPath); e != nil {
			t.Fatal(e)
		}
		if _, e := createS2CParticipant(c); e == nil {
			t.Fatal("symlink family")
		}
	})
}

func TestS2CParticipantExactOriginPersistence(t *testing.T) {
	f := s2cTestCluster(t, 3)
	c := s2cTestConfig(t, f, 1, t.TempDir())
	o, e := createS2CParticipant(c)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = o.Close() }()
	op := s1Operation(t, f.genesis.state.projection, testIdentity(), s1Changes(s1ReaderRole()))
	id := FullChangeID{S1Version, op.reviewed.Domain, op.reviewed.Cohort, 1, [16]byte{1}}
	raw := s2cTestSeal(t, f.trust, 1, f.keys[1], id, op, 1, false)
	original := append([]byte(nil), raw...)
	d, e := o.persistSealedOriginH(raw)
	if e != nil {
		t.Fatal(e)
	}
	p, b, e := o.Floors()
	if e != nil {
		t.Fatal(e)
	}
	raw[0] ^= 1
	if !bytes.Equal([]byte(o.origins[d].raw), original) {
		t.Fatal("retained borrowed input")
	}
	again, e := o.persistSealedOriginH(original)
	if e != nil || again != d || o.p.floor != p {
		t.Fatal("exact retransmission appended", e)
	}
	id.Nonce[0]++
	changed := s2cTestSeal(t, f.trust, 1, f.keys[1], id, op, 1, false)
	if _, e = o.persistSealedOriginH(changed); e == nil {
		t.Fatal("serial reused")
	}
	foreign := s2cTestSeal(t, f.trust, 2, f.keys[2], id, op, 2, false)
	if _, e = o.persistSealedOriginH(foreign); e == nil {
		t.Fatal("foreign origin persisted as owned")
	}
	if e = o.Close(); e != nil {
		t.Fatal(e)
	}
	o, e = resumeS2CParticipant(c, p, b)
	if e != nil {
		t.Fatal(e)
	}
	if got, e := o.persistSealedOriginH(original); e != nil || got != d || o.p.floor != p || o.pendingBytes != uint64(len(original)) {
		t.Fatal("restart lost immutable original", e)
	}
	if _, e = o.persistSealedOriginH(changed); e == nil {
		t.Fatal("restart forgot serial")
	}
}

func TestS2CParticipantCloseDrainsAdmission(t *testing.T) {
	f := s2cTestCluster(t, 3)
	c := s2cTestConfig(t, f, 1, t.TempDir())
	o, e := createS2CParticipant(c)
	if e != nil {
		t.Fatal(e)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	o.hooks = &s2cParticipantHooks{beforePAppend: func(kind byte) {
		if kind == s2cPBallot {
			close(entered)
			<-release
		}
	}}
	began := make(chan error, 1)
	go func() { _, e := o.Begin([32]byte{}); began <- e }()
	<-entered
	closed := make(chan error, 1)
	go func() { closed <- o.Close() }()
	deadline := time.Now().Add(5 * time.Second)
	for !o.admissionClosed.Load() {
		if time.Now().After(deadline) {
			t.Fatal("Close did not close admission")
		}
		runtime.Gosched()
	}
	select {
	case e := <-closed:
		t.Fatal("Close released resources during active append", e)
	default:
	}
	close(release)
	if e := <-began; e != nil {
		t.Fatal(e)
	}
	if e := <-closed; e != nil {
		t.Fatal(e)
	}
	if _, _, e = o.Floors(); !errors.Is(e, errS2CClosed) {
		t.Fatal("closed floors", e)
	}
	if _, e = o.Begin([32]byte{}); !errors.Is(e, errS2CClosed) {
		t.Fatal("closed dispatch", e)
	}
	if _, _, e = o.ReadLocalCut(); !errors.Is(e, errS2CClosed) {
		t.Fatal("closed B read", e)
	}
	resumed, e := resumeS2CParticipant(c, s2cJournalFloor{}, s2LocalReceipt{})
	if e != nil {
		t.Fatal("leases not released", e)
	}
	_ = resumed.Close()
}

type s2cPanicCloser struct{ io.Closer }

func (c s2cPanicCloser) Close() error { _ = c.Closer.Close(); panic("native closer panic") }

func TestS2CParticipantClosePanicStillReleasesBothFamilies(t *testing.T) {
	f := s2cTestCluster(t, 3)
	c := s2cTestConfig(t, f, 1, t.TempDir())
	o, e := createS2CParticipant(c)
	if e != nil {
		t.Fatal(e)
	}
	o.b.walOwner = s2cPanicCloser{o.b.walOwner}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("close panic swallowed")
			}
		}()
		_ = o.Close()
	}()
	if len(o.key) != 0 {
		t.Fatal("signer retained after failed close")
	}
	if _, _, e = o.Floors(); !errors.Is(e, errS2CUnknown) {
		t.Fatal("panic did not poison composite", e)
	}
	for _, path := range []string{c.PPath, c.BPath} {
		lease, e := mutationlog.AcquireFileWALLease(path)
		if e != nil {
			t.Fatal("panic stranded family lease", e)
		}
		_ = lease.Close()
	}
}

func TestS2CParticipantPendingChoiceRefusesOriginPersistence(t *testing.T) {
	n := s2cTestNativeCluster(t, 3, nil)
	n.selectValue(1, [32]byte{}, 1, 2)
	n.choose(1, 1, 2)
	o := n.nodes[3]
	// A private native checkpoint models a definite pre-I/O B-plan refusal. The
	// real signed choice must remain durable and pending, with no later P event.
	o.b.pending = &s2LocalPrepared{}
	chosen := n.take(s2cChosen, 1, 3)
	if out, e := o.Receive(chosen.raw); e == nil || !errors.Is(e, errS2BlockedSameDecision) || len(out) != 0 || o.chosen == nil || o.unknown != nil {
		t.Fatal("expected definite pending materialization", e)
	}
	before := o.p.floor
	op := s1Operation(t, n.fixture.genesis.state.projection, testIdentity(), s1Changes(s1ReaderRole()))
	id := FullChangeID{S1Version, op.reviewed.Domain, op.reviewed.Cohort, 1, [16]byte{19}}
	raw := s2cTestSeal(t, n.fixture.trust, 3, n.fixture.keys[3], id, op, 19, false)
	if digest, e := o.persistSealedOriginH(raw); !errors.Is(e, errS2CPending) || digest != [32]byte{} || o.p.floor != before || len(o.pending) != 0 {
		t.Fatal("origin ACK/append behind pending choice", e)
	}
	o.b.pending = nil
	n.restart(3)
	state, _, e := n.nodes[3].ReadLocalCut()
	if e != nil || state.slot != 1 {
		t.Fatal("blocked choice did not recover", e)
	}
}
