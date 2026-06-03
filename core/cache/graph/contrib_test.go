package graph

import "testing"

func TestContribID_IsZero(t *testing.T) {
	if !(ContribID{}).IsZero() {
		t.Errorf("zero value: IsZero=false, want true")
	}
	if (ContribID{0: 1}).IsZero() {
		t.Errorf("populated low byte: IsZero=true, want false")
	}
	if (ContribID{23: 1}).IsZero() {
		t.Errorf("populated high byte: IsZero=true, want false")
	}
}
