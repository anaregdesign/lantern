package security

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
)

func s2cRecordTestOwner(t *testing.T, f *s2cTestFixture, limit uint64) *s2cParticipant {
	t.Helper()
	c := s2cTestConfig(t, f, 1, t.TempDir())
	if limit != 0 {
		bounds := f.trust.bounds
		bounds.PayloadBytes = limit
		var err error
		c.Trust, err = newS2CTrust(s2cBootstrap{f.genesis, f.members, f.origins, bounds})
		if err != nil {
			t.Fatal(err)
		}
	}
	o, err := s2cNewParticipant(c)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestS2CRecordsCanonicalRoundTripAndCharge(t *testing.T) {
	f := s2cTestCluster(t, 3)
	o := s2cRecordTestOwner(t, f, 0)
	for kind := s2cPGenesis; kind <= s2cPDrained; kind++ {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			// The fixed envelope preserves all bits. The protocol replay owns
			// event-specific rules for these opaque metadata fields and body.
			r := s2cPRecord{
				kind: kind, index: math.MaxUint64, candidate: [32]byte{0x11, 0xff},
				credit:  s2cCredit{math.MaxUint64, 2, 12345, 1},
				receipt: s2LocalReceipt{[32]byte{1}, 888, [32]byte{2}, [32]byte{3}, 887, [32]byte{4}},
				raw:     "\x00exact body\xff\n",
			}
			if kind == s2cPDrained {
				r.raw = ""
			}
			raw, err := o.encodeRecord(r)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := o.decodeRecord(raw, r.index)
			if err != nil || decoded != r {
				t.Fatalf("record differs: %+v, %v", decoded, err)
			}
			again, err := o.encodeRecord(decoded)
			if err != nil || !bytes.Equal(again, raw) {
				t.Fatalf("record has noncanonical second encoding: %v", err)
			}
			charge, err := s2cPCharge(uint64(len(r.raw)))
			if err != nil || charge != uint64(len(raw))+88 {
				t.Fatalf("physical charge=%d; actual payload=%d: %v", charge, len(raw), err)
			}
			raw[len(raw)-1] ^= 1
			if decoded != r {
				t.Fatal("decoded record aliases input bytes")
			}
		})
	}
	for _, invalid := range []s2cPRecord{{kind: s2cPGenesis}, {kind: 0, index: 1}, {kind: s2cPDrained + 1, index: 1}} {
		if raw, err := o.encodeRecord(invalid); raw != nil || !errors.Is(err, errS2CProtocol) {
			t.Fatalf("encoded invalid envelope %+v: %v", invalid, err)
		}
	}
}

func TestS2CRecordsRejectMalformedEnvelope(t *testing.T) {
	f := s2cTestCluster(t, 3)
	o := s2cRecordTestOwner(t, f, 0)
	r := s2cPRecord{kind: s2cPAccept, index: 9, raw: "full retained value"}
	raw, err := o.encodeRecord(r)
	if err != nil {
		t.Fatal(err)
	}
	// Every truncation must fail without allowing a decoder slice panic.
	for n := 0; n < len(raw); n++ {
		if _, err := o.decodeRecord(raw[:n], r.index); !errors.Is(err, errS2CProtocol) {
			t.Fatalf("accepted truncation at byte %d: %v", n, err)
		}
	}
	indexOffset := len(s2cPMagic) + 1 + 32
	lengthOffset := len(raw) - len(r.raw) - 4
	cases := []struct {
		name   string
		change func([]byte) []byte
	}{
		{"magic", func(b []byte) []byte { b[0] ^= 1; return b }},
		{"scope", func(b []byte) []byte { b[len(s2cPMagic)+1] ^= 1; return b }},
		{"zero kind", func(b []byte) []byte { b[len(s2cPMagic)] = 0; return b }},
		{"unknown kind", func(b []byte) []byte { b[len(s2cPMagic)] = s2cPDrained + 1; return b }},
		{"zero index", func(b []byte) []byte { binary.BigEndian.PutUint64(b[indexOffset:], 0); return b }},
		{"different index", func(b []byte) []byte { binary.BigEndian.PutUint64(b[indexOffset:], r.index+1); return b }},
		{"short declared body", func(b []byte) []byte { binary.BigEndian.PutUint32(b[lengthOffset:], uint32(len(r.raw)-1)); return b }},
		{"long declared body", func(b []byte) []byte { binary.BigEndian.PutUint32(b[lengthOffset:], uint32(len(r.raw)+1)); return b }},
		{"overflowing declared body", func(b []byte) []byte { binary.BigEndian.PutUint32(b[lengthOffset:], math.MaxUint32); return b }},
		{"trailing byte", func(b []byte) []byte { return append(b, 0) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bad := tc.change(append([]byte(nil), raw...))
			if _, err := o.decodeRecord(bad, r.index); !errors.Is(err, errS2CProtocol) {
				t.Fatalf("accepted malformed %s: %v", tc.name, err)
			}
		})
	}
	if _, err := o.decodeRecord(raw, r.index+1); !errors.Is(err, errS2CProtocol) {
		t.Fatalf("decoder ignored physical sequence: %v", err)
	}
	other := s2cRecordTestOwner(t, f, 0)
	if _, err := other.decodeRecord(raw, r.index); !errors.Is(err, errS2CProtocol) {
		t.Fatalf("decoder adopted another local family: %v", err)
	}
}

func TestS2CRecordsBoundWholeApplicationPayload(t *testing.T) {
	f := s2cTestCluster(t, 3)
	r := s2cPRecord{kind: s2cPOrigin, index: 1, raw: strings.Repeat("h", 127)}
	full := s2cRecordTestOwner(t, f, 0)
	raw, err := full.encodeRecord(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, short := range []uint64{0, 1} {
		t.Run(fmt.Sprintf("short=%d", short), func(t *testing.T) {
			o := s2cRecordTestOwner(t, f, uint64(len(raw))-short)
			if uint64(len(r.raw)) >= o.trust.bounds.PayloadBytes {
				t.Fatal("fixture body alone must fit the common limit")
			}
			encoded, err := o.encodeRecord(r)
			if short == 0 && (err != nil || len(encoded) != len(raw)) || short != 0 && (encoded != nil || !errors.Is(err, errS2CProtocol)) {
				t.Fatalf("complete application bound: len=%d err=%v", len(encoded), err)
			}
			// Bind the otherwise identical record to this independently bootstrapped
			// family, so a scope mismatch cannot explain a decoder refusal.
			bound := append([]byte(nil), raw...)
			copy(bound[len(s2cPMagic)+1:], o.binding[:])
			_, err = o.decodeRecord(bound, r.index)
			if short == 0 && err != nil || short != 0 && !errors.Is(err, errS2CProtocol) {
				t.Fatalf("decoder complete application bound: %v", err)
			}
		})
	}
}

func TestS2CRecordsCompletionCreditAndOverflow(t *testing.T) {
	f := s2cTestCluster(t, 5)
	o := s2cRecordTestOwner(t, f, 0)
	preview := &s2cPreview{charge: 12345}
	credit, err := o.completionCredit(nil, preview)
	if err != nil {
		t.Fatal(err)
	}
	// These actual signatures measure the largest permitted fixed witness.
	// The test makes no claim that these fixture members crossed a WAL barrier.
	prepare, err := s2cSignPrepare(o.trust, f.keys[1], 1, 1, f.genesis.state.prefix, s2cBallot{1, 1})
	if err != nil {
		t.Fatal(err)
	}
	var promises []*s2cMessage
	for _, member := range f.members {
		promise, err := s2cSignPromise(o.trust, f.keys[member.ID], member.ID, prepare, s2cBallot{}, nil, [32]byte{})
		if err != nil {
			t.Fatal(err)
		}
		promises = append(promises, promise)
	}
	accept, err := s2cSignAccept(o.trust, f.keys[1], 1, prepare, promises, nil, [32]byte{0xad})
	if err != nil {
		t.Fatal(err)
	}
	var votes []*s2cMessage
	for _, member := range f.members {
		vote, err := s2cSignAccepted(o.trust, f.keys[member.ID], member.ID, accept, accept.admission)
		if err != nil {
			t.Fatal(err)
		}
		votes = append(votes, vote)
	}
	chosen, err := s2cMakeChosen(o.trust, accept, votes)
	if err != nil {
		t.Fatal(err)
	}
	chosenRecord, err := o.encodeRecord(s2cPRecord{kind: s2cPChosen, index: 1, raw: chosen.raw, credit: credit})
	if err != nil {
		t.Fatal(err)
	}
	drainedRecord, err := o.encodeRecord(s2cPRecord{kind: s2cPDrained, index: 2})
	if err != nil {
		t.Fatal(err)
	}
	want := s2cCredit{uint64(len(chosenRecord) + len(drainedRecord) + 2*88), 2, preview.charge, 1}
	if credit != want {
		t.Fatalf("completion reserve=%+v; actual largest witness and drain=%+v", credit, want)
	}
	for _, short := range []uint64{0, 1} {
		bounded := s2cRecordTestOwner(t, f, uint64(len(chosenRecord))-short)
		got, err := bounded.completionCredit(nil, preview)
		if short == 0 && (err != nil || got != credit) || short != 0 && !errors.Is(err, errS2CCapacity) {
			t.Fatalf("completion aggregate bound, short=%d: %+v, %v", short, got, err)
		}
	}
	old := s2cCredit{1000, 2, 500, 1}
	replacement := s2cCredit{800, 2, 700, 1}
	held := s2cMaxCredit(old, replacement)
	if held != (s2cCredit{1000, 2, 700, 1}) || s2cMaxCredit(held, s2cCredit{1, 1, 1, 1}) != held {
		t.Fatalf("smaller alternative refunded an old obligation: %+v", held)
	}
	maximal := s2cCredit{math.MaxUint64, math.MaxUint64, math.MaxUint64, math.MaxUint64}
	if s2cMaxCredit(held, maximal) != maximal {
		t.Fatal("credit maximum wrapped")
	}
	overhead, err := s2cPCharge(0)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s2cPCharge(math.MaxUint64 - overhead); err != nil || got != math.MaxUint64 {
		t.Fatalf("last representable charge=%d, %v", got, err)
	}
	for _, body := range []uint64{math.MaxUint64 - overhead + 1, math.MaxUint64} {
		if got, err := s2cPCharge(body); got != 0 || !errors.Is(err, errS2Unpersistable) {
			t.Fatalf("overflowing charge=%d, %v", got, err)
		}
	}
}

func TestS2CRecordsBCompletionCreditLimits(t *testing.T) {
	f := s2cTestCluster(t, 3)
	o := s2cRecordTestOwner(t, f, 0)
	o.config.BScope.Storage.JournalBytes, o.config.BScope.Storage.ReplayRecords = 1000, 10
	o.b = &s2LocalOwner{used: 900, receipt: s2LocalReceipt{LocalIndex: 9}}
	if !o.bCreditFits(s2cCredit{BBytes: 100, BRecords: 1}) {
		t.Fatal("exact remaining B bytes and record refused")
	}
	for _, credit := range []s2cCredit{{BBytes: 101, BRecords: 1}, {BBytes: 100, BRecords: 2}, {BBytes: math.MaxUint64}, {BRecords: math.MaxUint64}} {
		if o.bCreditFits(credit) {
			t.Fatalf("B completion credit exceeded physical bounds: %+v", credit)
		}
	}
	o.b.used = 1001
	if o.bCreditFits(s2cCredit{}) {
		t.Fatal("over-budget actual bytes underflowed remaining budget")
	}
	o.b.used, o.b.receipt.LocalIndex = 900, 11
	if o.bCreditFits(s2cCredit{}) {
		t.Fatal("over-budget actual records underflowed remaining count")
	}
}
