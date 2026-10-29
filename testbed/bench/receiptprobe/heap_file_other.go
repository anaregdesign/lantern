//go:build !linux && !darwin

package main

import (
	"errors"
	"os"
)

const retainedEvidenceSupported = false

// Only the measurement artifact reader is unsupported. Normal and historical
// snapshot evaluation keep their existing cross-platform behavior.
func openRetainedEvidence(string) (*os.File, error) {
	return nil, errors.New("retained measurement file custody requires Linux or Darwin")
}
func retainedEvidenceMetadataOK(os.FileInfo) bool                 { return false }
func retainedEvidenceMetadataEqual(os.FileInfo, os.FileInfo) bool { return false }
