package security

import (
	"testing"
	"time"
)

func TestAuthorityUTCLeapBounds(t *testing.T) {
	stamp := func(y int, m time.Month, day int) uint64 {
		return uint64(time.Date(y, m, day, 0, 0, 0, 0, time.UTC).UnixNano())
	}
	boundary := stamp(2028, time.March, 1) // leap-year February
	for _, tc := range []struct {
		name                     string
		start, low, high, events uint64
	}{
		{"before", boundary - 10e9, boundary - 5e9, boundary - 2e9, 0},
		{"negative can reach boundary", boundary - 10e9, boundary - 2e9, boundary - 1e9, 1},
		{"positive can repeat boundary", boundary - 10e9, boundary, boundary + 2e9, 1},
		{"anchor ambiguity", boundary + 1e9, boundary + 1e9, boundary + 2e9, 1},
		{"fresh after", boundary + 1e9 + 1, boundary + 2e9, boundary + 3e9, 0},
		{"two months", boundary - 10e9, stamp(2028, time.April, 2), stamp(2028, time.April, 2), 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := authorityUTCLeapBounds(tc.start, authorityTimeRange{tc.low, tc.high})
			if err != nil || r.low != tc.low-tc.events*uint64(time.Second) || r.high != tc.high+tc.events*uint64(time.Second) {
				t.Fatal(r, err)
			}
			for _, delta := range []int64{-1, 0, 1} {
				truth := uint64(int64(tc.low) + delta*int64(tc.events)*int64(time.Second))
				if truth < r.low || truth > r.high {
					t.Fatal("possible leap escaped")
				}
			}
		})
	}
}

func TestAuthorityTimeHoldoverHasNoPrecisionCliff(t *testing.T) {
	o, ticks := fakeAuthorityTimeOwner(t)
	initial := o.anchor.utc
	for _, hours := range []uint64{1, 24, 24 * 40} {
		ticks.Store(o.anchor.at.nanos + hours*uint64(time.Hour))
		now, err := o.current()
		if err != nil || now.utc.high-now.utc.low < initial.high-initial.low || now.sequence != 1 {
			t.Fatal("arbitrary holdover refusal", hours, err)
		}
	}
	// Native15s math has no UTC source/window premise.
	p := authorityTimePremises{ratePPB: 1_000_000, stampError: 1000}
	r, err := p.elapsed(fakeAuthorityTimeStamp(0), fakeAuthorityTimeStamp(uint64(15*time.Second)))
	if err != nil || r.low >= uint64(15*time.Second) || r.high <= uint64(15*time.Second) {
		t.Fatal("lease coupled to UTC policy", r, err)
	}
}
