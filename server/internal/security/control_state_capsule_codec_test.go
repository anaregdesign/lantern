package security

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"math"
	"testing"
)

func TestS2DecodeStrictCanonicalFramingAndRows(t *testing.T) {
	g, s, _, _ := s2TestHistory(t)
	encoded := s2TestEncode(t, s, g.roots)
	tests := []struct {
		name   string
		mutate func(*s2TestParts)
	}{
		{"unknown", func(p *s2TestParts) { p.header = append([]byte(`{"verified":true,`), p.header[1:]...) }},
		{"duplicate-field", func(p *s2TestParts) { p.header = append([]byte(`{"Version":1,`), p.header[1:]...) }},
		{"case-alias", func(p *s2TestParts) { p.header = bytes.Replace(p.header, []byte(`"Version"`), []byte(`"version"`), 1) }},
		{"missing-field", func(p *s2TestParts) { p.header = bytes.Replace(p.header, []byte(`"Version":1,`), nil, 1) }},
		{"whitespace", func(p *s2TestParts) { p.header = append([]byte(" "), p.header...) }},
		{"trailing-record", func(p *s2TestParts) { p.header = append(p.header, []byte("{}")...) }},
		{"noncanonical-number", func(p *s2TestParts) {
			p.header = bytes.Replace(p.header, []byte(`"Version":1`), []byte(`"Version":1.0`), 1)
		}},
		{"unknown-version", func(p *s2TestParts) {
			p.header = bytes.Replace(p.header, []byte(`"Version":1`), []byte(`"Version":2`), 1)
		}},
		{"duplicate-lineage-key", func(p *s2TestParts) { p.lineage = append(p.lineage, p.lineage[0]) }},
		{"reordered-lineage", func(p *s2TestParts) { p.lineage[0], p.lineage[1] = p.lineage[1], p.lineage[0] }},
		{"duplicate-ledger-key", func(p *s2TestParts) { p.ledger = append(p.ledger, p.ledger[0]) }},
		{"reordered-ledger", func(p *s2TestParts) { p.ledger[0], p.ledger[1] = p.ledger[1], p.ledger[0] }},
		{"duplicate-image-field", func(p *s2TestParts) { p.image = append([]byte(`{"version":2,`), p.image[1:]...) }},
		{"config-digest", func(p *s2TestParts) {
			var h s2CapsuleHeader
			_ = json.Unmarshal(p.header, &h)
			h.ConfigDigest = [32]byte{111}
			p.header = s2TestJSON(t, h)
		}},
		{"membership-scope", func(p *s2TestParts) {
			var h s2CapsuleHeader
			_ = json.Unmarshal(p.header, &h)
			h.Membership = [32]byte{111}
			p.header = s2TestJSON(t, h)
		}},
		{"floor-scope", func(p *s2TestParts) {
			var h s2CapsuleHeader
			_ = json.Unmarshal(p.header, &h)
			h.Floor.Domain = [32]byte{111}
			p.header = s2TestJSON(t, h)
		}},
		{"projection", func(p *s2TestParts) {
			var h s2CapsuleHeader
			_ = json.Unmarshal(p.header, &h)
			h.Cut.Projection = [32]byte{111}
			p.header = s2TestJSON(t, h)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := s2TestSplit(t, encoded)
			test.mutate(&p)
			if candidate, err := s2DecodeCapsule(p.encode(), s2TestLimits()); err == nil || candidate != nil {
				t.Fatal("noncanonical candidate escaped")
			}
		})
	}
	for _, b := range [][]byte{nil, {}, []byte("LNSEC03"), s.projection.snapshot.image, encoded[:len(encoded)-1], append(append([]byte(nil), encoded...), 0)} {
		if candidate, err := s2DecodeCapsule(b, s2TestLimits()); err == nil || candidate != nil {
			t.Fatal("invalid framing escaped")
		}
	}
}

func TestS2DecodeRejectsOriginalOperationAndItemDrift(t *testing.T) {
	g, s, _, _ := s2TestHistory(t)
	encoded := s2TestEncode(t, s, g.roots)
	tests := []struct {
		name   string
		mutate func(*s2CapsuleOutcome)
	}{
		{"missing-items", func(o *s2CapsuleOutcome) { o.Items = nil }},
		{"duplicate-index", func(o *s2CapsuleOutcome) { o.Items = append(o.Items, o.Items[0]) }},
		{"wrong-index", func(o *s2CapsuleOutcome) { o.Items[0].Index = 1 }},
		{"wrong-kind", func(o *s2CapsuleOutcome) { o.Items[0].Kind = PutRole }},
		{"unknown-outcome", func(o *s2CapsuleOutcome) { o.Disposition = "verified" }},
		{"mixed-item-outcome", func(o *s2CapsuleOutcome) { o.Items[0].Disposition = S1RejectedCAS }},
		{"original-digest", func(o *s2CapsuleOutcome) { o.Operation.Digest = [32]byte{120} }},
		{"observed-cut", func(o *s2CapsuleOutcome) { o.Observed.Frontier = [32]byte{121} }},
		{"actor", func(o *s2CapsuleOutcome) { o.Operation.Actor = s1Bob() }},
		{"intent-bytes", func(o *s2CapsuleOutcome) { o.Operation.Canonical += " " }},
		{"handoff-versus-first-commit", func(o *s2CapsuleOutcome) { o.Handoff = [32]byte{122} }},
		{"ID-scope", func(o *s2CapsuleOutcome) { o.ID.Cohort = [32]byte{123} }},
		{"first-slot", func(o *s2CapsuleOutcome) { o.Commit.Slot = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := s2TestSplit(t, encoded)
			var o s2CapsuleOutcome
			_ = json.Unmarshal(p.ledger[0], &o)
			test.mutate(&o)
			p.ledger[0] = s2TestJSON(t, o)
			if candidate, err := s2DecodeCapsule(p.encode(), s2TestLimits()); err == nil || candidate != nil {
				t.Fatal("operation/item drift escaped")
			}
		})
	}
	// Reject duplicate fields inside the exact original intent even when its
	// outer digest is replaced to match the attacker's new bytes.
	p := s2TestSplit(t, encoded)
	var o s2CapsuleOutcome
	_ = json.Unmarshal(p.ledger[0], &o)
	o.Operation.Canonical = string(bytes.Replace([]byte(o.Operation.Canonical), []byte(`{"Version":1,`), []byte(`{"Version":1,"Version":1,`), 1))
	o.Operation.Digest = sha256.Sum256([]byte(o.Operation.Canonical))
	p.ledger[0] = s2TestJSON(t, o)
	if c, err := s2DecodeCapsule(p.encode(), s2TestLimits()); err == nil || c != nil {
		t.Fatal("nested duplicate intent")
	}
}

func TestS2DecodeLengthCountAndAllocationGuards(t *testing.T) {
	g, s, _, _ := s2TestHistory(t)
	encoded := s2TestEncode(t, s, g.roots)
	badLength := append([]byte(nil), encoded...)
	binary.BigEndian.PutUint32(badLength[len(s2CapsuleMagic):], math.MaxUint32)
	if c, err := s2DecodeCapsule(badLength, s2TestLimits()); err == nil || c != nil {
		t.Fatal("length overflow")
	}
	p := s2TestSplit(t, encoded)
	countOffset := len(s2CapsuleMagic) + 8 + len(p.header) + len(p.image)
	badCount := append([]byte(nil), encoded...)
	binary.BigEndian.PutUint32(badCount[countOffset:], math.MaxUint32)
	if c, err := s2DecodeCapsule(badCount, s2TestLimits()); err == nil || c != nil {
		t.Fatal("count allocation guard")
	}
	if allocs := testing.AllocsPerRun(20, func() { r := s2CapsuleReader{[]byte{255, 255, 255, 255}}; _, _ = r.count(100000) }); allocs != 0 {
		t.Fatal("count guard allocated", allocs)
	}
	// A long digest array fails token preflight before typed fixed-array decode.
	p = s2TestSplit(t, encoded)
	p.header = bytes.Replace(p.header, []byte(`"Genesis":[`), []byte(`"Genesis":[0,`), 1)
	if c, err := s2DecodeCapsule(p.encode(), s2TestLimits()); err == nil || c != nil {
		t.Fatal("long fixed array")
	}
	p = s2TestSplit(t, encoded)
	// A short fixed array would silently zero-fill with ordinary encoding/json.
	var h map[string]json.RawMessage
	_ = json.Unmarshal(p.header, &h)
	h["Genesis"] = []byte(`[1]`)
	// Preserve header field order by replacing only the value.
	start := bytes.Index(p.header, []byte(`"Genesis":`)) + len(`"Genesis":`)
	end := start + bytes.IndexByte(p.header[start:], ']') + 1
	p.header = append(append(append([]byte(nil), p.header[:start]...), h["Genesis"]...), p.header[end:]...)
	if c, err := s2DecodeCapsule(p.encode(), s2TestLimits()); err == nil || c != nil {
		t.Fatal("short fixed array")
	}
	if err := s2JSONPreflight([]byte(`{"Changes":[`+string(bytes.Repeat([]byte(`0,`), MaxTransactionChanges))+`0]}`), DefaultPolicyLimits()); err == nil {
		t.Fatal("nested array guard")
	}
}

func TestS2DecodePreservesRequestOrderAndOriginalIntent(t *testing.T) {
	g, s, replay, _ := s2TestHistory(t)
	p := s2TestSplit(t, s2TestEncode(t, s, g.roots))
	var outcome s2CapsuleOutcome
	if err := json.Unmarshal(p.ledger[1], &outcome); err != nil || len(outcome.Items) != 2 {
		t.Fatal("ordered batch fixture", err)
	}
	outcome.Items[0], outcome.Items[1] = outcome.Items[1], outcome.Items[0]
	p.ledger[1] = s2TestJSON(t, outcome)
	if c, err := s2DecodeCapsule(p.encode(), s2TestLimits()); err == nil || c != nil {
		t.Fatal("reordered item identities")
	}
	p = s2TestSplit(t, s2TestEncode(t, s, g.roots))
	_ = json.Unmarshal(p.ledger[1], &outcome)
	command, err := s2DecodeOperation(outcome.Operation)
	if err != nil {
		t.Fatal(err)
	}
	command.Changes[0], command.Changes[1] = command.Changes[1], command.Changes[0]
	op, err := NewS1Operation(outcome.Operation.Actor, outcome.Operation.Reviewed, command, g.state.configuration.Policy)
	if err != nil {
		t.Fatal(err)
	}
	outcome.Operation.Canonical, outcome.Operation.Digest = op.canonical, op.digest
	p.ledger[1] = s2TestJSON(t, outcome)
	candidate, err := s2DecodeCapsule(p.encode(), s2TestLimits())
	if err != nil {
		t.Fatal("self-consistent reordered intent", err)
	}
	if state, err := s2RestoreCapsule(candidate, g, replay, s2TestLimits()); err == nil || state != nil {
		t.Fatal("reordered original intent restored")
	}
}

func FuzzS2DecodeCapsule(f *testing.F) {
	f.Add([]byte(s2CapsuleMagic))
	f.Add([]byte("LNSEC03"))
	p, err := NewS1Projection(s1Image(), DefaultPolicyLimits(), [32]byte{1}, [32]byte{2}, [32]byte{3}, [16]byte{4})
	if err != nil {
		f.Fatal(err)
	}
	s, err := NewS1ApplyState(p, [32]byte{5}, S1Capacity{100, 10, MaxImageBytes, 1 << 20}, S1Retention{})
	if err != nil {
		f.Fatal(err)
	}
	encoded, _, err := s2EncodeCapsule(s, s2TestGenesis(s).roots, s2TestLimits())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Fuzz(func(t *testing.T, encoded []byte) {
		if len(encoded) > 16384 {
			t.Skip()
		}
		limits := s2CapsuleLimits{16384, 32, 32}
		candidate, err := s2DecodeCapsule(encoded, limits)
		if err != nil && candidate != nil {
			t.Fatal("partial candidate")
		}
	})
}
