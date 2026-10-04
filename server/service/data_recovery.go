package service

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/anaregdesign/lantern/core/mutationlog"
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

// preflightDataNamespaceWAL runs under the path lease, before resuming any tip
// or clock journal. It validates the entire accepted-effect history and the
// latest active baseline; a rejected history must not advance durable bytes.
// Backup fallback supplies its own proven baseline and validates it separately.
func preflightDataNamespaceWAL(path string, config DurableReceiptWALRuntimeConfig, includeBaseline bool) error {
	var latest *receiptBaselineMarker
	err := mutationlog.ReplayFileWAL(path, decodeReceiptWALUnion, func(entry mutationlog.Entry) error {
		if err := validateReceiptWALUnionEntry(entry); err != nil {
			return err
		}
		if marker, ok := entry.Op.(receiptBaselineMarker); ok {
			latest = &marker
			return nil
		}
		mutation, ok := graphMutationFromLog(entry.Op)
		if !ok || mutation == nil || mutation.GetNamespaceFormat() != config.NamespaceFormat {
			return fmt.Errorf("WAL namespace format is incompatible at sequence %d", entry.Seq)
		}
		if config.NamespaceFormat != "" {
			if err := validatePhysicalDataIdentities(mutation.ProtoReflect()); err != nil {
				return fmt.Errorf("WAL crosses physical data boundary at sequence %d: %w", entry.Seq, err)
			}
		}
		return nil
	})
	if err != nil || latest == nil || !includeBaseline {
		return err
	}
	if config.BaselineCodec == nil {
		return errors.New("namespace proof requires baseline codec")
	}
	raw, err := (receiptBaselineSidecarStore{walPath: path}).load(latest.Format, latest.Digest, latest.Size)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, errReceiptBaselineSidecar) {
			return nil
		}
		return err
	} // normal recovery owns the narrowly eligible fallback classification
	stage, err := config.BaselineCodec.StageCombinedReceiptBaseline(context.Background(), raw, clearReceiptClockHighWater(config.Receipt), config.DefaultTTL, durableRuntimeGraphPolicy(config))
	if err != nil {
		return err
	}
	if stage == nil || stage.NamespaceFormat != config.NamespaceFormat {
		return errors.New("WAL baseline namespace format is incompatible")
	}
	return nil
}
