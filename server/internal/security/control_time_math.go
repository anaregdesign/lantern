package security

import (
	"errors"
	"math"
	"math/bits"
)

var errAuthorityTime = errors.New("unusable current-authority time measurement")

// These private values are measurement facts, not current-authority permits.
// No production admission path consumes them until the native/source profile
// has independently justified every premise below (#1722).
type authorityTimeStamp struct {
	boot    [32]byte
	process [16]byte
	nanos   uint64
}

type authorityTimeRange struct{ low, high uint64 }

// Mathematical hypotheses, deliberately not a qualified profile. All units are
// nanoseconds except the fractional rate error (parts per billion).
// sourceAllowance augments reported root distance and precision. Its
// sufficiency, and honest conservative reporting, are operational premises.
type authorityTimePremises struct {
	ratePPB, stampError, sourceAllowance uint64
	maxRTT, maxAge, maxWidth             uint64
	validUTC                             authorityTimeRange
	maxSourceError                       uint64
}

func (p authorityTimePremises) valid() bool {
	return p.ratePPB < 1_000_000_000 && p.stampError <= math.MaxUint64/2 &&
		p.sourceAllowance > 0 && p.maxSourceError > 0 && p.maxRTT > 0 && p.maxAge > 0 && p.maxWidth > 0 &&
		p.validUTC.low < p.validUTC.high
}

// Outward integer rounding, with checked 128-bit intermediate multiplication.
func authorityTimeScale(n, denominator uint64, up bool) (uint64, error) {
	hi, lo := bits.Mul64(n, 1_000_000_000)
	if denominator == 0 || hi >= denominator {
		return 0, errAuthorityTime
	}
	q, r := bits.Div64(hi, lo, denominator)
	if up && r != 0 {
		if q == math.MaxUint64 {
			return 0, errAuthorityTime
		}
		q++
	}
	return q, nil
}

func (p authorityTimePremises) elapsed(a, b authorityTimeStamp) (authorityTimeRange, error) {
	if !p.valid() || a.boot == [32]byte{} || a.process == [16]byte{} || a.boot != b.boot || a.process != b.process || b.nanos < a.nanos {
		return authorityTimeRange{}, errAuthorityTime
	}
	d, errorWidth := b.nanos-a.nanos, 2*p.stampError
	if d > math.MaxUint64-errorWidth {
		return authorityTimeRange{}, errAuthorityTime
	}
	lower := uint64(0)
	if d > errorWidth {
		lower = d - errorWidth
	}
	lo, err := authorityTimeScale(lower, 1_000_000_000+p.ratePPB, false)
	if err != nil {
		return authorityTimeRange{}, err
	}
	hi, err := authorityTimeScale(d+errorWidth, 1_000_000_000-p.ratePPB, true)
	if err != nil {
		return authorityTimeRange{}, err
	}
	return authorityTimeRange{lo, hi}, nil
}

// No local wall-clock era guessing. The era must be admitted independently;
// this partial implementation supports the current and immediately next era.
func authorityNTPUTC(raw uint64, era uint32) (authorityTimeRange, error) {
	if era > 1 || raw == 0 {
		return authorityTimeRange{}, errAuthorityTime
	}
	sec := uint64(era)<<32 | raw>>32
	if sec < 2_208_988_800 {
		return authorityTimeRange{}, errAuthorityTime
	}
	sec -= 2_208_988_800
	frac := (raw & math.MaxUint32) * 1_000_000_000
	lo := sec*1_000_000_000 + (frac >> 32)
	hi := lo
	if frac&math.MaxUint32 != 0 {
		hi++
	}
	return authorityTimeRange{lo, hi}, nil
}

// A conditional estimate cannot authorize an operation. It retains the original
// receive sample; repeatedly evaluating it never refreshes its age.
type authorityTimeEstimate struct {
	at       authorityTimeStamp
	utc      authorityTimeRange
	premises authorityTimePremises
}

func calculateAuthorityTime(m authorityTimeMeasurement, p authorityTimePremises) (authorityTimeEstimate, error) {
	d, err := p.elapsed(m.sent, m.received)
	if err != nil || d.high >= p.maxRTT {
		return authorityTimeEstimate{}, errAuthorityTime
	}
	errorBound, err := p.sourceBound(m.packet)
	if err != nil {
		return authorityTimeEstimate{}, err
	}
	s, err := authorityNTPInRange(m.packet.transmit, p.validUTC)
	if err != nil || s.low < errorBound || s.high > math.MaxUint64-errorBound {
		return authorityTimeEstimate{}, errAuthorityTime
	}
	r, err := authorityNTPInRange(m.packet.receive, p.validUTC)
	if err != nil || r.low > s.high {
		return authorityTimeEstimate{}, errAuthorityTime
	}
	ref, err := authorityNTPInRange(m.packet.reference, p.validUTC)
	if err != nil || ref.low > s.high {
		return authorityTimeEstimate{}, errAuthorityTime
	}
	hi := s.high + errorBound
	if hi > math.MaxUint64-d.high {
		return authorityTimeEstimate{}, errAuthorityTime
	}
	// A genuine causal response transmits between the local send and receive
	// samples. Full RTT covers either direction, processing, and local pauses.
	a := authorityTimeEstimate{m.received, authorityTimeRange{s.low - errorBound, hi + d.high}, p}
	if !p.contains(a.utc) {
		return authorityTimeEstimate{}, errAuthorityTime
	}
	return a, nil
}

func (p authorityTimePremises) contains(r authorityTimeRange) bool {
	return p.valid() && r.low <= r.high && r.low >= p.validUTC.low &&
		r.high < p.validUTC.high && r.high-r.low < p.maxWidth
}

func (a authorityTimeEstimate) atSample(now authorityTimeStamp) (authorityTimeRange, error) {
	d, err := a.premises.elapsed(a.at, now)
	if err != nil || d.high >= a.premises.maxAge || !a.premises.contains(a.utc) ||
		a.utc.high > math.MaxUint64-d.high {
		return authorityTimeRange{}, errAuthorityTime
	}
	r := authorityTimeRange{a.utc.low + d.low, a.utc.high + d.high}
	if !a.premises.contains(r) {
		return authorityTimeRange{}, errAuthorityTime
	}
	return r, nil
}

func authorityNTPInRange(raw uint64, window authorityTimeRange) (authorityTimeRange, error) {
	var result authorityTimeRange
	matches := 0
	for era := uint32(0); era <= 1; era++ {
		r, err := authorityNTPUTC(raw, era)
		if err == nil && r.low >= window.low && r.high < window.high {
			result = r
			matches++
		}
	}
	if matches != 1 {
		return authorityTimeRange{}, errAuthorityTime
	}
	return result, nil
}

func (p authorityTimePremises) sourceBound(packet authorityNTPPacket) (uint64, error) {
	if !p.valid() || packet.reportedRootDelay < 0 {
		return 0, errAuthorityTime
	}
	dispersion, err := authorityTimeScale(uint64(packet.reportedRootDispersion), 1<<16, true)
	if err != nil {
		return 0, err
	}
	delay, err := authorityTimeScale(uint64(packet.reportedRootDelay), 1<<17, true)
	if err != nil {
		return 0, err
	}
	precision := uint64(1)
	exponent := int(packet.reportedPrecision)
	if exponent >= 0 {
		if exponent > 34 {
			return 0, errAuthorityTime
		}
		precision = 1_000_000_000 << exponent
	} else if exponent > -64 {
		precision, err = authorityTimeScale(1, uint64(1)<<(-exponent), true)
		if err != nil {
			return 0, err
		}
	}
	bound := p.sourceAllowance
	for _, term := range []uint64{dispersion, delay, precision} {
		if term > math.MaxUint64-bound {
			return 0, errAuthorityTime
		}
		bound += term
	}
	if bound > p.maxSourceError {
		return 0, errAuthorityTime
	}
	return bound, nil
}
