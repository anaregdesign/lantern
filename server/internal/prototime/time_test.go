package prototime

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestExpiration(t *testing.T) {
	t.Run("AbsentIsPermanent", func(t *testing.T) {
		if got := Expiration(nil); !got.IsZero() {
			t.Fatalf("Expiration(nil) = %v, want zero", got)
		}
	})

	t.Run("ExplicitUnixEpochStaysExplicit", func(t *testing.T) {
		want := time.Unix(0, 0).UTC()
		if got := Expiration(timestamppb.New(want)); !got.Equal(want) {
			t.Fatalf("Expiration(epoch) = %v, want %v", got, want)
		}
	})
}
