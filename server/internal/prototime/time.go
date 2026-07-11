// Package prototime converts Protobuf Well-Known time values without
// collapsing an absent message into the Unix epoch.
package prototime

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// Expiration returns the absolute timestamp, or the zero time when the wire
// field is absent. In Lantern the zero time means permanent storage.
func Expiration(value *timestamppb.Timestamp) time.Time {
	if value == nil {
		return time.Time{}
	}
	return value.AsTime()
}
