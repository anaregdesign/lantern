package security

import (
	"crypto/sha256"
	"encoding/binary"
	"sync/atomic"
	"testing"
	"time"
)

// Shared deterministic facts. These assumptions are test inputs, not an
// admission of the actual Mac oscillator, Apple source or Internet path.
func fakeAuthorityTimeStamp(n uint64) authorityTimeStamp {
	return authorityTimeStamp{boot: [32]byte{1}, process: [16]byte{2}, nanos: n}
}

func fakeAuthorityTimePremises() authorityTimePremises {
	return authorityTimePremises{
		ratePPB: 1_000_000, stampError: 1, sourceAllowance: 999, maxSourceError: 1_000_000_000,
		maxRTT: 1_000_000_000, maxAge: 10_000_000_000, maxWidth: 2_000_000_000,
		validUTC: authorityTimeRange{1_577_836_800_000_000_000, 3_471_292_800_000_000_000},
	}
}

func fakeAuthorityNTPResponse(nonce [8]byte) [48]byte {
	var response [48]byte
	response[0], response[1] = 4<<3|4, 1
	response[3] = 226 // precision 2^-30 seconds.
	copy(response[24:32], nonce[:])
	binary.BigEndian.PutUint64(response[16:24], uint64(3_999_999_999)<<32)
	binary.BigEndian.PutUint64(response[32:40], uint64(4_000_000_000)<<32)
	binary.BigEndian.PutUint64(response[40:48], uint64(4_000_000_000)<<32)
	return response
}

func fakeAuthorityTimeOwner(t *testing.T) (*authorityTimeOwner, *atomic.Uint64) {
	t.Helper()
	ticks := new(atomic.Uint64)
	ticks.Store(100_000_000)
	source, _ := parseAuthorityTimeSource([]byte("server time.asia.apple.com"))
	p := &authorityTimeProducer{source: source, sample: func() (authorityTimeStamp, error) { return fakeAuthorityTimeStamp(ticks.Load()), nil }}
	o := &authorityTimeOwner{producer: p, premises: authorityOperationalTimePremises(), profile: sha256.Sum256([]byte(authorityTimeProfileDescription))}
	nonce := [8]byte{1}
	raw := fakeAuthorityNTPResponse(nonce)
	packet, err := parseAuthorityNTP(raw[:], nonce)
	if err != nil {
		t.Fatal(err)
	}
	m := authorityTimeMeasurement{source: source, sent: fakeAuthorityTimeStamp(0), received: fakeAuthorityTimeStamp(ticks.Load()), packet: packet, sequence: 1}
	if err = o.installLocked(m); err != nil {
		t.Fatal(err)
	}
	return o, ticks
}

func fakeAuthorityTimeOwnerAt(t *testing.T, utc time.Time) (*authorityTimeOwner, *atomic.Uint64) {
	t.Helper()
	o, ticks := fakeAuthorityTimeOwner(t)
	// Deliberate interval fixture for TLS/current-authority unit tests; this
	// is not the separate native raw-producer qualification campaign.
	o.anchor.utc = authorityTimeRange{uint64(utc.Add(-10 * time.Millisecond).UnixNano()), uint64(utc.Add(10 * time.Millisecond).UnixNano())}
	return o, ticks
}
