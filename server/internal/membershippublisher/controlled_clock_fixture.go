//go:build lantern_test_time && (linux || darwin)

package membershippublisher

import (
	"testing"
	"time"
)

// OpenControlledFixture shares one test-only clock across initial admission,
// publication and delivery observations. It grants no native-time authority.
// Normal builds exclude this constructor; tagged non-test binaries refuse it.
func OpenControlledFixture(configPath, statePath, mode string, clock func() time.Time) (*Publisher, error) {
	if !testing.Testing() || clock == nil || mode != "init" && mode != "resume" {
		return nil, ErrState
	}
	return openOwnedWithClock(configPath, statePath, mode, clock().UTC(), nil, clock)
}
