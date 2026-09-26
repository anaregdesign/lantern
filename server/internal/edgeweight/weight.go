package edgeweight

import "math"

// IsFiniteSource checks a Put base or Add contribution, not a derived
// effective weight or an authoritative receipt result.
func IsFiniteSource(weight float32) bool {
	return weight >= -math.MaxFloat32 && weight <= math.MaxFloat32
}
