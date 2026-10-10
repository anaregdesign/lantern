package security

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"testing"
)

func TestAuthorityHistoricalVersionAndBounds(t *testing.T) {
	for _, count := range []int{3, 31} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			f, keys := authorityTestFixture(t, count)
			h, op := authorityTestHeader(t, f)
			raw := authorityTestSealHeader(t, f, keys[1], h, op)
			v, err := verifyHistoricalH(f.trust, raw)
			if err != nil || v.raw != string(raw) || v.handoff.operation != op {
				t.Fatal(err)
			}
			headerBytes := binary.BigEndian.Uint32(raw[len(authorityHistoricalMagic):])
			t.Logf("voters=%d H_bytes=%d header_bytes=%d certificate_bytes=%d", count, len(raw), headerBytes, len(h.Renewal))
			var decoded authorityHistoricalHeader
			if err := json.Unmarshal(raw[len(authorityHistoricalMagic)+4:len(authorityHistoricalMagic)+4+int(headerBytes)], &decoded); err != nil || !bytes.Equal(decoded.Renewal, h.Renewal) {
				t.Fatal("binary certificate was text normalized", err)
			}
			legacy := s2cTestCluster(t, count)
			if _, err := verifyHistoricalH(legacy.trust, raw); err == nil {
				t.Fatal("v2 adopted in v1 scope")
			}
			for _, offset := range []int{0, len(authorityHistoricalMagic) - 1, len(authorityHistoricalMagic), len(raw) - 1} {
				bad := bytes.Clone(raw)
				bad[offset] ^= 1
				if _, err := verifyHistoricalH(f.trust, bad); err == nil {
					t.Fatal("tampered frame", offset)
				}
			}
			for _, n := range []int{0, len(authorityHistoricalMagic) + 3, len(raw) - 1} {
				if _, err := verifyHistoricalH(f.trust, raw[:n]); err == nil {
					t.Fatal("truncated frame", n)
				}
			}
			bounds := f.trust.bounds
			bounds.HeaderBytes = uint64(headerBytes) - 1
			if _, err := authorityHistoricalBody(decoded, op.Encode(), bounds); err == nil {
				t.Fatal("header ceiling ignored")
			}
		})
	}
}

func TestAuthorityHistoricalTimeAssociation(t *testing.T) {
	for name, edit := range map[string]func(*authorityHistoricalHeader){
		"time profile":        func(h *authorityHistoricalHeader) { h.Time.Profile[0] ^= 1 },
		"missing host epoch":  func(h *authorityHistoricalHeader) { h.Time.HostBoot = [32]byte{} },
		"other receiver boot": func(h *authorityHistoricalHeader) { h.Time.Process[0] ^= 1 },
		"sequence absent":     func(h *authorityHistoricalHeader) { h.Time.SourceSequence = 0 },
		"counter regression":  func(h *authorityHistoricalHeader) { h.Time.Counter = h.Time.StartCounter - 1 },
		"lifetime expired":    func(h *authorityHistoricalHeader) { h.Time.Counter += authorityRenewalLifetime },
		"UTC range":           func(h *authorityHistoricalHeader) { h.Time.UTCLow = 0 },
		"UTC inverted":        func(h *authorityHistoricalHeader) { h.Time.UTCLow = h.Time.UTCHigh + 1 },
		"width equality": func(h *authorityHistoricalHeader) {
			h.Time.UTCHigh = h.Time.UTCLow + authorityOperationalTimePremises().maxWidth
		},
		"fake scalar consume":   func(h *authorityHistoricalHeader) { h.ConsumeUpper = h.ConsumeUpper.Add(-1) },
		"workload binding":      func(h *authorityHistoricalHeader) { h.Workloads[0] ^= 1 },
		"quorum signature":      func(h *authorityHistoricalHeader) { h.Renewal[len(h.Renewal)-1] ^= 1 },
		"wrong retry namespace": func(h *authorityHistoricalHeader) { h.ID.Namespace++ },
	} {
		t.Run(name, func(t *testing.T) {
			f, keys := authorityTestFixture(t, 3)
			h, op := authorityTestHeader(t, f)
			edit(&h)
			if _, err := verifyHistoricalH(f.trust, authorityTestSealHeader(t, f, keys[1], h, op)); err == nil {
				t.Fatal("invalid current attestation")
			}
		})
	}
}

func TestAuthorityHistoricalNativeReservationRequired(t *testing.T) {
	n, keys := authorityTestNativeCluster(t, 3)
	o := n.nodes[1]
	h, op := authorityTestHeader(t, n.fixture)
	raw := authorityTestSealHeader(t, n.fixture, keys[1], h, op)
	if _, err := o.persistSealedOriginH(raw); err == nil {
		t.Fatal("v2 H without reservation")
	}
	o.gate.Lock()
	reservation, err := o.reserveOriginLocked(h.ID, op.digest, s2cCredit{})
	o.gate.Unlock()
	if err != nil || reservation.Serial != h.Serial {
		t.Fatal(err)
	}
	digest, err := o.persistSealedOriginH(raw)
	if err != nil {
		t.Fatal(err)
	}
	n.restart(1)
	o = n.nodes[1]
	if o.originSerial != h.Serial || o.origins[digest].raw != string(raw) {
		t.Fatal("recovered H changed")
	}
	before := o.p.floor
	if again, err := o.persistSealedOriginH(raw); err != nil || again != digest || o.p.floor != before {
		t.Fatal("exact original H retried by resealing", err)
	}
	// The same bytes can first choose/apply from historical time, without any
	// new purpose or time owner. Current disclosure remains a separate path.
	n.selectValue(1, digest, 1, 2)
	n.choose(1, 1, 2)
	state, outcome, _, err := o.LookupOriginal(h.ID)
	if err != nil || state != S1Known || outcome == nil {
		t.Fatal(state, err)
	}
}
