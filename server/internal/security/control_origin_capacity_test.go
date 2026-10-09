package security

import (
	"testing"
)

func TestAuthorityOriginCapacityDominatesFinalEncoding(t *testing.T) {
	n, keys := authorityTestNativeCluster(t, 3)
	o := n.nodes[1]
	h, op := authorityTestHeader(t, n.fixture)
	a, err := AssessS1(o.replayState.projection, op)
	if err != nil {
		t.Fatal(err)
	}
	protected, bound, err := authorityOriginCapacity(o, h, op, a)
	if err != nil {
		t.Fatal(err)
	}
	for _, sequence := range []uint64{1, 255, 9999999999999999999, ^uint64(0)} {
		h.Time.SourceSequence = sequence
		raw := authorityTestSealHeader(t, n.fixture, keys[1], h, op)
		v, err := verifyHistoricalH(o.trust, raw)
		if err != nil || uint64(len(raw)) > bound {
			t.Fatal("H bound", len(raw), bound, err)
		}
		preview, err := s2cPreviewCandidate(o.trust, o.replayState, o.trust.genesis.roots, o.config.BScope, o.replayReceipt, v, s2cLogicalCommit(o.trust, 1, v.digest()))
		if err != nil {
			t.Fatal(err)
		}
		actual, err := o.completionCredit(v, preview)
		hCharge, _ := s2cPCharge(uint64(len(raw)))
		if err != nil || protected.PBytes < actual.PBytes+hCharge || protected.PRecords < actual.PRecords+1 || protected.BBytes < actual.BBytes || protected.BRecords != actual.BRecords {
			t.Fatal("completion bound underestimated", protected, actual, err)
		}
	}
	o.config.PendingBytes = bound - 1
	if _, _, err := authorityOriginCapacity(o, h, op, a); err == nil {
		t.Fatal("bounded pending pool exceeded")
	}
	if o.p.count != 2 || o.originSerial != 0 {
		t.Fatal("sizing wrote durable state")
	}
}
