//go:build lantern_test_time && (linux || darwin)

package provider

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestControlledNativeWindowBoundsRefusesUnavailableWithoutHostFallback(t *testing.T) {
	for _, runtime := range []*SecurityRuntime{nil, {}, {mode: "off"}, {mode: "oidc"}, {mode: "oidc", current: &security.CurrentAuthority{}}} {
		if runtime != nil {
			runtime.now = func() time.Time { t.Fatal("host fallback consulted"); return time.Time{} }
		}
		if low, high, err := runtime.ControlledNativeWindowTimeBounds(t.Context()); err == nil || !low.IsZero() || !high.IsZero() {
			t.Fatal("unavailable owned clock produced time", low, high, err)
		}
	}
	for name, observe := range map[string]func() (time.Time, time.Time, error){
		"missing": nil,
		"closed": func() (time.Time, time.Time, error) {
			return time.Time{}, time.Time{}, security.ErrAuthorityUnavailable
		},
		"reversed": func() (time.Time, time.Time, error) {
			return time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC), time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), nil
		},
		"zero": func() (time.Time, time.Time, error) { return time.Time{}, time.Time{}, nil },
	} {
		t.Run(name, func(t *testing.T) {
			if low, high, err := controlledNativeWindowBounds(t.Context(), observe); !errors.Is(err, security.ErrAuthorityUnavailable) || !low.IsZero() || !high.IsZero() {
				t.Fatal("unavailable bounds accepted", low, high, err)
			}
		})
	}
}

func TestControlledNativeWindowBoundsIndependentOfManagementReadiness(t *testing.T) {
	// Only an owned-clock observation is a dependency. In particular no Ready
	// callback, chosen-management state or host timestamp can select these facts.
	low := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	high := low.Add(3 * time.Millisecond)
	calls := 0
	observe := func() (time.Time, time.Time, error) { calls++; return low, high, nil }
	gotLow, gotHigh, err := controlledNativeWindowBounds(t.Context(), observe)
	if err != nil || !gotLow.Equal(low) || !gotHigh.Equal(high) || calls != 1 {
		t.Fatal("borrowed bounds changed", gotLow, gotHigh, calls, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := controlledNativeWindowBounds(ctx, observe); err == nil || calls != 1 {
		t.Fatal("canceled observer sampled clock", calls, err)
	}
}
