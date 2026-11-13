//go:build lantern_test_time && (linux || darwin)

package provider

import (
	"context"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/security"
)

// ControlledNativeWindowTimeBounds observes only the existing OIDC owner's
// borrowed clock. It neither opens an owner nor reads Ready: a chosen management
// CAS may withdraw public readiness without invalidating that native clock.
// This method does not exist in ordinary builds, and tagged non-test binaries
// refuse it just as the controlled-time constructor does.
func (r *SecurityRuntime) ControlledNativeWindowTimeBounds(ctx context.Context) (time.Time, time.Time, error) {
	if !testing.Testing() || r == nil || r.mode != "oidc" || r.current == nil {
		return time.Time{}, time.Time{}, security.ErrAuthorityUnavailable
	}
	view := r.current.WorkloadTime()
	defer view.Close() // A borrowed view cannot close its authority.
	return controlledNativeWindowBounds(ctx, view.TimeBounds)
}

func controlledNativeWindowBounds(ctx context.Context, observe func() (time.Time, time.Time, error)) (time.Time, time.Time, error) {
	if ctx.Err() != nil || observe == nil {
		return time.Time{}, time.Time{}, security.ErrAuthorityUnavailable
	}
	low, high, err := observe()
	if err != nil || ctx.Err() != nil || low.IsZero() || high.Before(low) || low.Year() < 2020 || high.Year() >= 2080 {
		return time.Time{}, time.Time{}, security.ErrAuthorityUnavailable
	}
	return low.UTC(), high.UTC(), nil
}
