package security

import (
	"math"
	"math/big"
	"testing"
)

func TestAuthorityTimeScale(t *testing.T) {
	for _, n := range []uint64{0, 1, 2, 999_999_999, 1_000_000_000, 1 << 32, 1 << 63, math.MaxUint64 - 1, math.MaxUint64} {
		for _, denominator := range []uint64{0, 1, 999_999_999, 1_000_000_000, 1_000_000_001, 1_999_999_999} {
			for _, up := range []bool{false, true} {
				got, err := authorityTimeScale(n, denominator, up)
				if denominator == 0 {
					if err == nil {
						t.Fatal("zero divisor accepted")
					}
					continue
				}
				want := new(big.Int).Mul(new(big.Int).SetUint64(n), big.NewInt(1_000_000_000))
				if up {
					want.Add(want, new(big.Int).SetUint64(denominator-1))
				}
				want.Div(want, new(big.Int).SetUint64(denominator))
				if !want.IsUint64() {
					if err == nil {
						t.Fatal("overflow accepted")
					}
					continue
				}
				if err != nil || got != want.Uint64() {
					t.Fatalf("n=%d divisor=%d up=%v: got %d %v want %s", n, denominator, up, got, err, want)
				}
			}
		}
	}
}

func TestAuthorityTimeElapsed(t *testing.T) {
	p := fakeAuthorityTimePremises()
	got, err := p.elapsed(fakeAuthorityTimeStamp(0), fakeAuthorityTimeStamp(1_000_000_000))
	if err != nil || got != (authorityTimeRange{999000997, 1001001004}) {
		t.Fatalf("outward interval %+v %v", got, err)
	}
	for name, alter := range map[string]func(*authorityTimePremises, *authorityTimeStamp, *authorityTimeStamp){
		"different boot":    func(_ *authorityTimePremises, _, b *authorityTimeStamp) { b.boot[1] = 1 },
		"different process": func(_ *authorityTimePremises, _, b *authorityTimeStamp) { b.process[1] = 1 },
		"unknown epoch":     func(_ *authorityTimePremises, a, _ *authorityTimeStamp) { a.boot = [32]byte{} },
		"regression":        func(_ *authorityTimePremises, a, b *authorityTimeStamp) { a.nanos, b.nanos = 2, 1 },
		"no lower rate":     func(p *authorityTimePremises, _, _ *authorityTimeStamp) { p.ratePPB = 1_000_000_000 },
		"delta overflow":    func(_ *authorityTimePremises, _, b *authorityTimeStamp) { b.nanos = math.MaxUint64 },
		"error overflow":    func(p *authorityTimePremises, _, _ *authorityTimeStamp) { p.stampError = math.MaxUint64 },
	} {
		t.Run(name, func(t *testing.T) {
			q, a, b := p, fakeAuthorityTimeStamp(0), fakeAuthorityTimeStamp(1)
			alter(&q, &a, &b)
			if _, err := q.elapsed(a, b); err == nil {
				t.Fatal("invalid elapsed accepted")
			}
		})
	}
}

func TestAuthorityNTPUTC(t *testing.T) {
	for _, tc := range []struct {
		raw  uint64
		era  uint32
		want authorityTimeRange
		good bool
	}{
		{uint64(2_208_988_800) << 32, 0, authorityTimeRange{0, 0}, true},
		{uint64(2_208_988_801)<<32 | 1, 0, authorityTimeRange{1_000_000_000, 1_000_000_001}, true},
		{uint64(2_208_988_801)<<32 | 1<<31, 0, authorityTimeRange{1_500_000_000, 1_500_000_000}, true},
		{uint64(math.MaxUint32)<<32 | math.MaxUint32, 0, authorityTimeRange{2085978495999999999, 2085978496000000000}, true},
		{1 << 32, 1, authorityTimeRange{2085978497000000000, 2085978497000000000}, true},
		{0, 0, authorityTimeRange{}, false},
		{1 << 32, 0, authorityTimeRange{}, false},
		{1 << 32, 2, authorityTimeRange{}, false},
	} {
		got, err := authorityNTPUTC(tc.raw, tc.era)
		if (err == nil) != tc.good || got != tc.want {
			t.Fatalf("%+v: %+v %v", tc, got, err)
		}
	}
}

func TestAuthorityTimeEstimate(t *testing.T) {
	p := fakeAuthorityTimePremises()
	p.ratePPB, p.stampError = 0, 0
	nonce := [8]byte{1}
	raw := fakeAuthorityNTPResponse(nonce)
	packet, err := parseAuthorityNTP(raw[:], nonce)
	if err != nil {
		t.Fatal(err)
	}
	m := authorityTimeMeasurement{sent: fakeAuthorityTimeStamp(0), received: fakeAuthorityTimeStamp(100_000_000), packet: packet}
	a, err := calculateAuthorityTime(m, p)
	if err != nil {
		t.Fatal(err)
	}
	s := uint64(1_791_011_200_000_000_000)
	if a.utc != (authorityTimeRange{s - 1000, s + 100_001_000}) {
		t.Fatalf("anchor: %+v", a.utc)
	}
	// Every asymmetric division of the same measured RTT fits, including
	// all delay on one side and local pauses before sending/after receiving.
	for _, returnDelay := range []uint64{0, 1, 25_000_000, 50_000_000, 100_000_000} {
		for _, sourceOffset := range []int64{-1000, 0, 1000} {
			truth := uint64(int64(s)+sourceOffset) + returnDelay
			if truth < a.utc.low || truth > a.utc.high {
				t.Fatal("asymmetric path escaped interval")
			}
		}
	}
	now := fakeAuthorityTimeStamp(1_100_000_000)
	got, err := a.atSample(now)
	if err != nil || got != (authorityTimeRange{a.utc.low + 1_000_000_000, a.utc.high + 1_000_000_000}) {
		t.Fatalf("holdover: %+v %v", got, err)
	}
	for i := 0; i < 3; i++ {
		if _, err := a.atSample(fakeAuthorityTimeStamp(m.received.nanos + p.maxAge)); err == nil {
			t.Fatal("equality expiry or repeated read reset accepted")
		}
	}
	if _, err := a.atSample(fakeAuthorityTimeStamp(m.received.nanos + p.maxAge - 1)); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*authorityTimePremises, *authorityTimeMeasurement){
		"RTT equality":        func(p *authorityTimePremises, m *authorityTimeMeasurement) { p.maxRTT = m.received.nanos },
		"too wide":            func(p *authorityTimePremises, _ *authorityTimeMeasurement) { p.maxWidth = 100 },
		"UTC expiry equality": func(p *authorityTimePremises, _ *authorityTimeMeasurement) { p.validUTC.high = a.utc.high },
		"unknown era": func(p *authorityTimePremises, _ *authorityTimeMeasurement) {
			p.validUTC = authorityTimeRange{0, math.MaxUint64}
		},
		"server backwards": func(_ *authorityTimePremises, m *authorityTimeMeasurement) { m.packet.receive += 1 << 32 },
		"source underflow": func(p *authorityTimePremises, _ *authorityTimeMeasurement) { p.sourceAllowance = math.MaxUint64 },
	} {
		t.Run(name, func(t *testing.T) {
			q, n := p, m
			mutate(&q, &n)
			if _, err := calculateAuthorityTime(n, q); err == nil {
				t.Fatal("invalid estimate accepted")
			}
		})
	}
	t.Run("holdover rate widens uncertainty", func(t *testing.T) {
		p.ratePPB, p.maxWidth = 100_000_000, 200_000_000
		a, err := calculateAuthorityTime(m, p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.atSample(fakeAuthorityTimeStamp(1_100_000_000)); err == nil {
			t.Fatal("excess holdover uncertainty accepted")
		}
	})
}

func TestAuthorityTimeRateEndpoints(t *testing.T) {
	for _, rho := range []uint64{0, 1, 1_000_000, 999_999_999} {
		for _, elapsed := range []uint64{1, 1000, 1_000_000_000, 10_000_000_000} {
			for _, rate := range []uint64{1_000_000_000 - rho, 1_000_000_000 + rho} {
				// Independent exact arithmetic simulates the extreme oscillator;
				// floor quantization is covered by the per-sample uncertainty.
				delta := new(big.Int).Mul(new(big.Int).SetUint64(elapsed), new(big.Int).SetUint64(rate))
				delta.Div(delta, big.NewInt(1_000_000_000))
				p := fakeAuthorityTimePremises()
				p.ratePPB = rho
				r, err := p.elapsed(fakeAuthorityTimeStamp(0), fakeAuthorityTimeStamp(delta.Uint64()))
				upper := new(big.Int).Add(new(big.Int).Set(delta), new(big.Int).SetUint64(2*p.stampError))
				upper.Mul(upper, big.NewInt(1_000_000_000))
				upper.Add(upper, new(big.Int).SetUint64(1_000_000_000-rho-1))
				upper.Div(upper, new(big.Int).SetUint64(1_000_000_000-rho))
				if !upper.IsUint64() {
					if err == nil {
						t.Fatal("unrepresentable elapsed accepted")
					}
					continue
				}
				if err != nil || r.low > elapsed || r.high < elapsed {
					t.Fatalf("rho=%d elapsed=%d rate=%d interval=%+v %v", rho, elapsed, rate, r, err)
				}
			}
		}
	}
}

func TestAuthorityTimeSourceBoundsAndEra(t *testing.T) {
	p := authorityOperationalTimePremises()
	packet := authorityNTPPacket{reportedRootDispersion: 1 << 14, reportedRootDelay: 1 << 15, reportedPrecision: -2}
	bound, err := p.sourceBound(packet)
	if err != nil || bound != 760_000_000 {
		t.Fatalf("source interval %d %v", bound, err)
	}
	for _, mutate := range []func(*authorityNTPPacket){
		func(n *authorityNTPPacket) { n.reportedRootDelay = -1 },
		func(n *authorityNTPPacket) { n.reportedRootDispersion = math.MaxUint32 },
		func(n *authorityNTPPacket) { n.reportedPrecision = 127 },
	} {
		n := packet
		mutate(&n)
		if _, err := p.sourceBound(n); err == nil {
			t.Fatal("invalid source bound")
		}
	}
	for _, raw := range []uint64{uint64(math.MaxUint32)<<32 | math.MaxUint32, 1, 1 << 32} {
		r, err := authorityNTPInRange(raw, p.validUTC)
		if err != nil || r.low < 2_085_978_495_000_000_000 || r.high > 2_085_978_498_000_000_000 {
			t.Fatalf("era boundary %x: %+v %v", raw, r, err)
		}
	}
	nonce := [8]byte{1}
	raw := fakeAuthorityNTPResponse(nonce)
	n, err := parseAuthorityNTP(raw[:], nonce)
	if err != nil {
		t.Fatal(err)
	}
	n.receive = uint64(math.MaxUint32)<<32 | math.MaxUint32
	n.transmit = 1
	m := authorityTimeMeasurement{packet: n, sent: fakeAuthorityTimeStamp(0), received: fakeAuthorityTimeStamp(1000)}
	if _, err := calculateAuthorityTime(m, p); err != nil {
		t.Fatal("ordered exchange across era rollover", err)
	}
	if p.contains(authorityTimeRange{p.validUTC.low, p.validUTC.low + p.maxWidth}) {
		t.Fatal("width equality accepted")
	}
}
