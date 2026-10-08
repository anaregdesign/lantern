package security

import (
	"errors"
	"os"
	"testing"

	"github.com/anaregdesign/lantern/core/mutationlog"
)

func TestS2CRestoreRefusesIncompleteCreate(t *testing.T) {
	f := s2cTestCluster(t, 3)
	c := s2cTestConfig(t, f, 1, t.TempDir())
	c.PPolicy.Records = 1
	c.PendingCount = 1
	if o, e := createS2CParticipant(c); e == nil {
		_ = o.Close()
		t.Fatal("bootstrap without DRAINED exposed")
	}
	for _, p := range []string{c.PPath, c.PPath + ".tip", c.BPath, c.BPath + ".tip"} {
		if _, e := os.Stat(p); e != nil {
			t.Fatal("expected retained incomplete family", p, e)
		}
	}
	if o, e := resumeS2CParticipant(c, s2cJournalFloor{}, s2LocalReceipt{}); e == nil {
		_ = o.Close()
		t.Fatal("incomplete Create resumed")
	}
	// Construction cleanup must release every partially acquired native lease.
	for _, p := range []string{c.PPath, c.BPath} {
		lease, e := mutationlog.AcquireFileWALLease(p)
		if e != nil {
			t.Fatal("construction leaked lease", e)
		}
		if e = lease.Close(); e != nil {
			t.Fatal(e)
		}
	}
}

func TestS2CRestoreNeverCreatesMissingFamily(t *testing.T) {
	f := s2cTestCluster(t, 3)
	for _, name := range []string{"P", "P.tip", "P.lease", "B", "B.tip", "B.lease"} {
		t.Run(name, func(t *testing.T) {
			c := s2cTestConfig(t, f, 1, t.TempDir())
			o, e := createS2CParticipant(c)
			if e != nil {
				t.Fatal(e)
			}
			p, b, e := o.Floors()
			if e != nil {
				t.Fatal(e)
			}
			if e = o.Close(); e != nil {
				t.Fatal(e)
			}
			paths := map[string]string{"P": c.PPath, "P.tip": c.PPath + ".tip", "P.lease": c.PPath + ".lease", "B": c.BPath, "B.tip": c.BPath + ".tip", "B.lease": c.BPath + ".lease"}
			missing := paths[name]
			if e = os.Remove(missing); e != nil {
				t.Fatal(e)
			}
			if o, e = resumeS2CParticipant(c, p, b); e == nil {
				_ = o.Close()
				t.Fatal("missing family resumed")
			}
			if _, e = os.Stat(missing); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("Resume created missing identity family", e)
			}
		})
	}
}

func TestS2CRestoreRejectsUnfundedAcceptedRecord(t *testing.T) {
	n := s2cTestNativeCluster(t, 3, nil)
	n.selectValue(1, [32]byte{}, 1, 2)
	envelope := n.take(s2cAccept, 1, 3)
	m, e := s2cDecodeMessage(n.fixture.trust, envelope.raw)
	if e != nil {
		t.Fatal(e)
	}
	o := n.nodes[3]
	preview, e := s2cPreviewCandidate(o.trust, o.b.state, o.trust.genesis.roots, o.config.BScope, o.b.receipt, m.h, s2cLogicalCommit(o.trust, m.slot, m.value))
	if e != nil {
		t.Fatal(e)
	}
	credit, e := o.completionCredit(m.h, preview)
	if e != nil {
		t.Fatal(e)
	}
	credit.PBytes--
	// Inject a CRC-correct, signed-protocol record through the real file journal.
	// It cannot qualify because its recorded durable completion contract is short.
	raw, e := o.encodeRecord(s2cPRecord{kind: s2cPAccept, index: o.p.count + 1, credit: credit, raw: m.raw})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = o.p.append(raw, 0, 0); e != nil {
		t.Fatal(e)
	}
	p, b, e := o.Floors()
	if e != nil {
		t.Fatal(e)
	}
	n.stop(3)
	before, e := os.ReadFile(n.configs[3].PPath)
	if e != nil {
		t.Fatal(e)
	}
	if restored, e := resumeS2CParticipant(n.configs[3], p, b); e == nil {
		_ = restored.Close()
		t.Fatal("unfunded ACCEPT recovered")
	}
	after, e := os.ReadFile(n.configs[3].PPath)
	if e != nil || string(before) != string(after) {
		t.Fatal("invalid suffix repaired/truncated", e)
	}
}

func TestS2CRestoreManifestDoesNotResetLocalIdentity(t *testing.T) {
	f := s2cTestCluster(t, 3)
	c := s2cTestConfig(t, f, 1, t.TempDir())
	o, e := createS2CParticipant(c)
	if e != nil {
		t.Fatal(e)
	}
	p, b, e := o.Floors()
	if e != nil {
		t.Fatal(e)
	}
	if e = o.Close(); e != nil {
		t.Fatal(e)
	}
	for name, change := range map[string]func(*s2cParticipantConfig){
		"P_epoch":          func(c *s2cParticipantConfig) { c.PEpoch[0]++ },
		"P_identity":       func(c *s2cParticipantConfig) { c.PIdentity[0]++ },
		"B_epoch":          func(c *s2cParticipantConfig) { c.BScope.JournalEpoch[0]++ },
		"P_budget":         func(c *s2cParticipantConfig) { c.PPolicy.Bytes-- },
		"B_budget":         func(c *s2cParticipantConfig) { c.BScope.Storage.JournalBytes-- },
		"origin_ownership": func(c *s2cParticipantConfig) { c.OwnedOrigin = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			altered := c
			change(&altered)
			if restored, e := resumeS2CParticipant(altered, p, b); e == nil {
				_ = restored.Close()
				t.Fatal("identity or local policy reset accepted")
			}
		})
	}
	restored, e := resumeS2CParticipant(c, p, b)
	if e != nil {
		t.Fatal("failed attempts leaked ownership", e)
	}
	_ = restored.Close()
}
