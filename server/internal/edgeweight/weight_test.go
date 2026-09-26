package edgeweight

import (
	"math"
	"testing"
)

func TestIsFiniteSource(t *testing.T) {
	for _, tc := range []struct {
		name   string
		weight float32
		want   bool
	}{
		{"zero", 0, true},
		{"negative zero", math.Float32frombits(1 << 31), true},
		{"maximum", math.MaxFloat32, true},
		{"minimum", -math.MaxFloat32, true},
		{"NaN", float32(math.NaN()), false},
		{"positive infinity", float32(math.Inf(1)), false},
		{"negative infinity", float32(math.Inf(-1)), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsFiniteSource(tc.weight); got != tc.want {
				t.Fatalf("IsFiniteSource(%v) = %t, want %t", tc.weight, got, tc.want)
			}
		})
	}
}
