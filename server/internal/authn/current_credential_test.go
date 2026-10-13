package authn

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestCurrentCodeStartWaitPreservesStrictNativeBounds(t *testing.T) {
	now := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	calls := 0
	bounds := func() (time.Time, time.Time, error) {
		calls++
		if calls == 1 {
			return now.Add(-time.Nanosecond), now.Add(time.Millisecond), nil
		}
		return now, now.Add(time.Millisecond), nil
	}
	if err := waitCurrentCodeStart(t.Context(), bounds, now, now.Add(time.Hour)); err != nil || calls != 2 {
		t.Fatal("Code start accepted before lower endpoint or not rechecked", calls, err)
	}
	for _, kind := range []string{"expired", "uncertain", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			sampled := 0
			bound := func() (time.Time, time.Time, error) {
				sampled++
				switch kind {
				case "expired":
					return now.Add(-time.Nanosecond), now.Add(time.Hour), nil
				case "uncertain":
					return time.Time{}, time.Time{}, security.ErrAuthorityUnavailable
				default:
					cancel()
					return now.Add(-time.Second), now.Add(time.Millisecond), nil
				}
			}
			err := waitCurrentCodeStart(ctx, bound, now, now.Add(time.Hour))
			if err == nil || sampled != 1 {
				t.Fatal("wait invented eligibility", sampled, err)
			}
			if kind == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}
