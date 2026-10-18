package security

import (
	"testing"
	"time"
)

func TestAuthorityTimeRefreshSchedule(t *testing.T) {
	for _, tc := range []struct {
		width uint64
		want  time.Duration
	}{{3_000_000_000, 699997 * time.Millisecond}, {100_000_000, 2149995550 * time.Microsecond}} {
		a := authorityTimeEstimate{at: fakeAuthorityTimeStamp(0), premises: authorityOperationalTimePremises(), utc: authorityTimeRange{1792281600e9, 1792281600e9 + tc.width}}
		latest := authorityTimeRefreshDelay(a, 0)
		if latest < tc.want-time.Millisecond || latest > tc.want+time.Millisecond {
			t.Fatal("prediction", latest, tc.want)
		}
		for _, random := range []uint64{1, 123456789, ^uint64(0)} {
			d := authorityTimeRefreshDelay(a, random)
			if d > latest || d < latest-latest/10 {
				t.Fatal("jitter did not stay earlier", d, latest)
			}
		}
	}
}
