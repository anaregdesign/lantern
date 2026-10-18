package security

import (
	"errors"
	"time"
)

// Operational goals only. Neither width nor a timer authorizes/refuses a
// request: consumers must contain the full interval in their actual windows.
const authorityTimeRefreshGoal = uint64(5 * time.Second)
const authorityTimeRefreshLead = 5 * time.Minute

func authorityTimeRefreshDelay(a authorityTimeEstimate, random uint64) time.Duration {
	// With the admitted1000ppm envelope, the target is crossed within an
	// hour even for a point anchor. The day cap bounds scheduling arithmetic
	// for test hypotheses; it is never a semantic holdover limit.
	lo, hi := uint64(0), uint64(24*time.Hour)
	for lo < hi {
		mid := lo + (hi-lo)/2
		stamp := a.at
		wide := stamp.nanos > ^uint64(0)-mid
		if !wide {
			stamp.nanos += mid
			r, err := a.atSample(stamp)
			wide = err != nil || r.high-r.low >= authorityTimeRefreshGoal
		}
		if wide {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	delay := time.Duration(lo) - authorityTimeRefreshLead
	delay = max(delay, time.Minute)
	return max(time.Minute, delay-time.Duration(random%uint64(delay/10+1)))
}

type authorityTimeRetry struct {
	backoff   time.Duration
	qualified bool
}

func (s *authorityTimeRetry) delay(err error, refresh time.Duration) time.Duration {
	if err == nil {
		s.backoff, s.qualified = 0, true
		return refresh
	}
	if errors.Is(err, errAuthorityTimeRate) || s.backoff >= 1024*time.Second {
		s.backoff = 1024 * time.Second
		return s.backoff
	}
	if s.backoff == 0 {
		s.backoff = 32 * time.Second // finite45s initial construction budget
		if s.qualified {
			s.backoff = time.Minute
		}
	} else {
		s.backoff = min(s.backoff*2, 15*time.Minute)
	}
	return s.backoff
}
