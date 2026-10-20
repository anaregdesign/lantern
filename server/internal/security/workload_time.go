package security

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anaregdesign/lantern/server/decisiontrace"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

// WorkloadTime is a narrow native-time lease. OFF owns its clock; OIDC views
// enter the live authority on each operation and cannot outlive or close it.
type WorkloadTime struct {
	clock     *authorityTimeOwner
	authority *CurrentAuthority
	closed    atomic.Bool
	once      sync.Once
}

// NativeWorkloadTimeTarget classifies build targets, not runtime qualification.
// An eligible target's acquisition failure must never select scalar fallback.
func NativeWorkloadTimeTarget() bool {
	return nativeWorkloadTimeTarget(runtime.GOOS, runtime.GOARCH)
}

func OpenWorkloadTime(ctx context.Context) (_ *WorkloadTime, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	clock, err := newCurrentAuthorityTimeOwner()
	if err != nil {
		return nil, err
	}
	if err = waitAuthorityTimeAnchor(ctx, clock); err != nil {
		clock.close()
		return nil, err
	}
	return &WorkloadTime{clock: clock}, nil
}

func (o *CurrentAuthority) WorkloadTime() *WorkloadTime {
	return &WorkloadTime{authority: o}
}

func (w *WorkloadTime) withClock(call func(*authorityTimeOwner) error) error {
	if w == nil || w.closed.Load() {
		return ErrAuthorityUnavailable
	}
	if w.authority != nil {
		o := w.authority
		if o.origin == nil || o.origin.network == nil || !o.origin.network.enterCall() {
			return ErrAuthorityUnavailable
		}
		defer o.origin.network.calls.Done()
		return call(o.origin.network.timeOwner)
	}
	if w.clock == nil {
		return ErrAuthorityUnavailable
	}
	return call(w.clock)
}

func (w *WorkloadTime) Observe() (observation peerauth.TimeObservation, err error) {
	err = w.withClock(func(clock *authorityTimeOwner) error {
		current, e := clock.current()
		if e == nil {
			observation = workloadObservation{w, current}
		}
		return e
	})
	return observation, err
}

func (w *WorkloadTime) TimeBounds() (time.Time, time.Time, error) {
	observation, err := w.Observe()
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	low, high := observation.Bounds()
	return low, high, nil
}

func (w *WorkloadTime) VerificationTime() time.Time {
	_, high, err := w.TimeBounds()
	if err != nil {
		return time.Unix(0, int64(^uint64(0)>>1)).UTC()
	}
	return high
}

func (w *WorkloadTime) TimeAcquisitionCounts() (counts decisiontrace.AcquisitionCounts) {
	_ = w.withClock(func(clock *authorityTimeOwner) error {
		counts = clock.acquisitionCounts()
		return nil
	})
	return counts
}

func (w *WorkloadTime) Close() error {
	if w != nil {
		w.once.Do(func() {
			w.closed.Store(true)
			if w.authority == nil && w.clock != nil {
				w.clock.close()
			}
		})
	}
	return nil
}

type workloadObservation struct {
	view    *WorkloadTime
	current authorityCurrentTime
}

func (o workloadObservation) Bounds() (time.Time, time.Time) {
	return time.Unix(0, int64(o.current.utc.low)).UTC(), time.Unix(0, int64(o.current.utc.high)).UTC()
}

func (o workloadObservation) ElapsedSince(prior peerauth.TimeObservation) (elapsed time.Duration, err error) {
	start, ok := prior.(workloadObservation)
	if !ok || start.view != o.view {
		return 0, ErrAuthorityUnavailable
	}
	err = o.view.withClock(func(clock *authorityTimeOwner) error {
		bounds, e := clock.elapsed(start.current, o.current)
		if e == nil {
			elapsed = time.Duration(bounds.high)
		}
		return e
	})
	return elapsed, err
}

func nativeWorkloadTimeTarget(goos, arch string) bool {
	return goos == "linux" && (arch == "amd64" || arch == "arm64") || goos == "darwin" && arch == "arm64"
}
