// Package prototime converts Protobuf Well-Known time values without
// collapsing an absent message into the Unix epoch.
package prototime

import (
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// Expiration returns the absolute timestamp, or the zero time when the wire
// field is absent. In Lantern the zero time means permanent storage. Validate
// untrusted wire values with CheckedExpiration before using this conversion.
func Expiration(value *timestamppb.Timestamp) time.Time {
	if value == nil {
		return time.Time{}
	}
	return value.AsTime()
}

// CheckedExpiration validates a wire deadline before converting it. The
// protobuf minimum timestamp is Go's zero time; accepting that explicit
// instant would make an expired value indistinguishable from an omitted,
// permanent expiration.
func CheckedExpiration(value *timestamppb.Timestamp) (time.Time, error) {
	if value == nil {
		return time.Time{}, nil
	}
	if err := value.CheckValid(); err != nil {
		return time.Time{}, fmt.Errorf("invalid expiration timestamp: %w", err)
	}
	expiration := value.AsTime()
	if expiration.IsZero() {
		return time.Time{}, fmt.Errorf("explicit expiration equals the no-expiration zero-time sentinel")
	}
	return expiration, nil
}
