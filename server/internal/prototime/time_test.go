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

func TestCheckedExpiration(t *testing.T) {
	cases := []struct {
		name    string
		value   *timestamppb.Timestamp
		want    time.Time
		wantErr bool
	}{
		{"absent", nil, time.Time{}, false},
		{"epoch", timestamppb.New(time.Unix(0, 0).UTC()), time.Unix(0, 0).UTC(), false},
		{"pre-epoch", timestamppb.New(time.Unix(-1, 0).UTC()), time.Unix(-1, 0).UTC(), false},
		{"first positive fraction", timestamppb.New(time.Unix(0, 500_000_000).UTC()), time.Unix(0, 500_000_000).UTC(), false},
		{"future", timestamppb.New(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)), time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), false},
		{"explicit Go zero", timestamppb.New(time.Time{}), time.Time{}, true},
		{"out-of-range seconds", &timestamppb.Timestamp{Seconds: 253402300800}, time.Time{}, true},
		{"invalid nanos", &timestamppb.Timestamp{Nanos: 1_000_000_000}, time.Time{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CheckedExpiration(tc.value)
			if (err != nil) != tc.wantErr || (!tc.wantErr && !got.Equal(tc.want)) {
				t.Fatalf("CheckedExpiration(%v) = (%v, %v), want (%v, error=%v)", tc.value, got, err, tc.want, tc.wantErr)
			}
		})
	}
}
