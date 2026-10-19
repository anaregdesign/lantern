package security

import (
	"math"
	"time"
)

// RFC5905 section7.3 permits +/-1s leaps at month-end. NTP/Unix timestamps
// retain no leap phase. Include any boundary whose one-second ambiguity region
// can intersect the path from the original anchor to the forecast. Widen both
// endpoints by one second per possible event, including an event that a
// negative leap could bring into reach. No wall clock, leap-file lookup or
// monthly availability cutoff is involved. LI warnings still invalidate live
// acquisition separately; LI=0 is not a promise of no future leap.
func authorityUTCLeapBounds(start uint64, nominal authorityTimeRange) (authorityTimeRange, error) {
	const second = uint64(time.Second)
	if start > nominal.high || nominal.low > nominal.high || nominal.high > math.MaxInt64 {
		return authorityTimeRange{}, errAuthorityTime
	}
	at := time.Unix(0, int64(start)).UTC()
	// Include the preceding boundary when the anchor is in its ambiguity
	// region. Dates are arithmetic over admitted UTC, never time.Now().
	boundary := time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
	if uint64(boundary.UnixNano())+second < start {
		boundary = boundary.AddDate(0, 1, 0)
	}
	allowance := uint64(0)
	for {
		b := boundary.UnixNano()
		if b < int64(second) || nominal.high > math.MaxInt64-allowance {
			return authorityTimeRange{}, errAuthorityTime
		}
		if uint64(b)-second > nominal.high+allowance {
			break
		}
		if allowance > math.MaxInt64-second {
			return authorityTimeRange{}, errAuthorityTime
		}
		allowance += second
		boundary = boundary.AddDate(0, 1, 0)
	}
	if nominal.low < allowance || nominal.high > math.MaxInt64-allowance {
		return authorityTimeRange{}, errAuthorityTime
	}
	return authorityTimeRange{nominal.low - allowance, nominal.high + allowance}, nil
}
