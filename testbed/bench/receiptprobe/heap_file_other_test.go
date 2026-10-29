//go:build !linux && !darwin

package main

import "testing"

func TestRetainedEvidenceUnsupportedPlatformRefuses(t *testing.T) {
	if retainedEvidenceSupported {
		t.Fatal("unsupported measurement platform declared supported")
	}
	if file, err := openRetainedEvidence("unopened-evidence.json"); err == nil || file != nil {
		t.Fatal("unsupported measurement read did not fail closed")
	}
}
