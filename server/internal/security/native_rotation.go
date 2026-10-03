package security

import (
	"context"
	"errors"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
)

type rotationFaultPoint uint8

const (
	rotationAfterCheckpoint rotationFaultPoint = iota + 1
	rotationAfterManifest
	rotationAfterCleanup
)

func (j *nativeJournal) checkRotationFault(point rotationFaultPoint) error {
	if j.rotationFault != nil {
		return j.rotationFault(point)
	}
	return nil
}

// rotate is serialized with Store publication and holds the stable path lease.
// It syncs a complete originally signed checkpoint and its lower-bound tip,
// syncs the new selector, then removes obsolete segments before another write.
// Any failure poisons serving; recovery follows only the selected complete cut.
func (j *nativeJournal) rotate(ctx context.Context, used, nextBytes int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	current := j.store.current.Load()
	if current == nil || !current.completeCheckpointHistory() {
		return ErrInvalidRevision
	}
	checkpointBytes := int64(8 + len(current.encoded) + systemFrameBytes + systemTipHeaderBytes + systemTipRecordBytes)
	// Bound the transient old+new files and reserve room for the pending change.
	if used+checkpointBytes+nextBytes > j.maxBytes {
		return ErrSystemJournalCapacity
	}
	manifest, err := newNativeManifest(j.binding, current)
	if err != nil {
		return err
	}
	path := manifest.path(j.anchor)
	log, owner, err := createNativeSegment(path, j.binding)
	if err != nil {
		return err
	}
	selected := false
	defer func() {
		if !selected {
			_ = owner.Close()
		}
	}()
	if _, err := log.Append(current, hlc.Timestamp{}); err != nil {
		return err
	}
	if err := j.checkRotationFault(rotationAfterCheckpoint); err != nil {
		return err
	}
	if err := writeNativeManifest(j.anchor, manifest, false); err != nil {
		return err
	}
	// The selector is now durable. Never append to the previous segment again.
	oldOwner, oldTip := j.owner, j.tip
	j.tip = nil
	j.base = manifest.base
	if err := j.attach(log, owner, path); err != nil {
		return errors.Join(err, oldOwner.Close(), closeSystemTip(oldTip))
	}
	selected = true
	if err := errors.Join(oldOwner.Close(), closeSystemTip(oldTip)); err != nil {
		return err
	}
	if err := j.checkRotationFault(rotationAfterManifest); err != nil {
		return err
	}
	if err := cleanupNativeSegments(j.anchor, path); err != nil {
		return err
	}
	return j.checkRotationFault(rotationAfterCleanup)
}

func closeSystemTip(tip *mutationlog.FileWALTipJournal) error {
	if tip == nil {
		return nil
	}
	return tip.Close()
}
