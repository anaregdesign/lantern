package dataruntime

import (
	"errors"
	"fmt"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// ValidateDataSnapshotNamespace proves the classified physical graph boundary
// before a detached snapshot can be installed. A logical-key archive cannot be
// reinterpreted by guessing whether its keys happen to start with data:.
func ValidateDataSnapshotNamespace(frames []*pb.SnapshotResponse, expected string) error {
	if err := validateDataFormat(expected); err != nil {
		return err
	}
	if len(frames) == 0 || frames[0] == nil || frames[0].GetHeader() == nil || frames[0].GetHeader().GetNamespaceFormat() != expected {
		return errors.New("snapshot namespace format is incompatible")
	}
	for _, frame := range frames {
		if frame == nil {
			return errors.New("nil snapshot frame")
		}
		if expected != "" {
			if err := validatePhysicalDataIdentities(frame.ProtoReflect()); err != nil {
				return fmt.Errorf("snapshot crosses physical data boundary: %w", err)
			}
		}
	}
	return nil
}
