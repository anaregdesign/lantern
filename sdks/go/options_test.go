package client

import "testing"

// TestWithBatchChunkSize_ClampedToContribIndexSpace pins that
// WithBatchChunkSize clamps to the uint16 contrib-ID index space (65536) so
// no single chunk can wrap the per-chunk index and collide two contributions
// (#919), while preserving the existing non-positive → default fallback.
func TestWithBatchChunkSize_ClampedToContribIndexSpace(t *testing.T) {
	cases := []struct {
		in   int
		want int
	}{
		{in: 70000, want: 1 << 16},   // above the uint16 contrib-id index space: clamped
		{in: 1 << 16, want: 1 << 16}, // exactly the ceiling: kept
		{in: 1000, want: 1000},       // below the ceiling: kept verbatim
	}
	for _, tc := range cases {
		var o options
		WithBatchChunkSize(tc.in)(&o)
		if o.batchChunkSize != tc.want {
			t.Fatalf("WithBatchChunkSize(%d) → %d, want %d", tc.in, o.batchChunkSize, tc.want)
		}
	}

	// Non-positive values are ignored (the clamp must not disturb this): the
	// field is left at its zero value here, and NewLantern seeds the default
	// before applying options, so 0/negative fall through to defaultBatchChunkSize.
	for _, in := range []int{0, -1, -65536} {
		var o options
		WithBatchChunkSize(in)(&o)
		if o.batchChunkSize != 0 {
			t.Fatalf("WithBatchChunkSize(%d) must not set the field (got %d); non-positive keeps the default", in, o.batchChunkSize)
		}
	}
}
