package security

import (
	"context"
	"errors"

	"github.com/anaregdesign/lantern/core/hlc"
)

// InstallCheckpoint is replica-only catch-up. It requires both the original
// complete signed revision and a fresh challenge-bound lease for that exact
// cut. Writer recovery never promotes a replica checkpoint into authority.
func (n *NativeStore) InstallCheckpoint(ctx context.Context, encoded []byte, proof *CheckpointProof) error {
	if n == nil || n.store.faulted.Load() {
		return ErrStoreUnavailable
	}
	store := n.store
	if len(store.privateKey) != 0 {
		return ErrReadOnlyWriter
	}
	revision, err := DecodeRevision(encoded, store.publicKey, store.limits)
	if err != nil {
		return err
	}
	if revision.generation != store.generation || !revision.completeCheckpointHistory() {
		return ErrInvalidRevision
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.faulted.Load() {
		return ErrStoreUnavailable
	}
	if err := proof.check(ctx, store, revision); err != nil {
		return err
	}
	current := store.current.Load()
	if current != nil {
		if current.sequence == revision.sequence && current.digest == revision.digest {
			return nil
		}
		if revision.sequence <= current.sequence || revision.snapshot.Image().BootstrapRevision < current.snapshot.Image().BootstrapRevision {
			return ErrRevisionConflict
		}
		if revision.sequence == current.sequence+1 && revision.previous != current.digest {
			return ErrRevisionConflict
		}
		for _, history := range revision.history {
			if history.record.result.Revision == current.sequence && history.record.result.Digest != current.digest {
				return ErrRevisionConflict
			}
		}
		if revision.snapshot.Image().BootstrapRevision == current.snapshot.Image().BootstrapRevision && (revision.snapshot.Image().BootstrapDigest != current.snapshot.Image().BootstrapDigest || !sameEnvOwned(current.snapshot.Image(), revision.snapshot.Image())) {
			return ErrBootstrapLocked
		}
	}
	complete := false
	defer func() {
		if !complete {
			store.faulted.Store(true)
		}
	}()
	expected := [32]byte{}
	if current != nil {
		expected = current.digest
	}
	if err := n.journal.installCheckpoint(ctx, revision, expected); err != nil {
		return errors.Join(ErrStoreUnavailable, err)
	}
	store.changes = make(map[[16]byte]changeRecord)
	store.order = nil
	for _, history := range revision.history {
		store.changes[history.id] = history.record
		store.order = append(store.order, history.id)
	}
	store.publish(revision)
	complete = true
	return nil
}
func (j *nativeJournal) installCheckpoint(ctx context.Context, revision *Revision, expected [32]byte) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed || j.log == nil {
		return ErrStoreUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	witness, err := j.provenance.TipWitness(j.path)
	if err != nil {
		return err
	}
	used, err := systemJournalSize(witness.Offset, witness.Seq)
	if err != nil {
		return err
	}
	checkpointBytes := int64(8 + len(revision.encoded) + systemFrameBytes + systemTipHeaderBytes + systemTipRecordBytes + systemManifestBytes)
	if used+checkpointBytes > j.maxBytes {
		return ErrSystemJournalCapacity
	}
	stage, err := j.metadata.PrepareCheckpoint(expected, revision.sequence, revision.encoded)
	if err != nil {
		return err
	}
	defer stage.Abort()
	manifest, err := newNativeManifest(j.binding, revision)
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
	if _, err := log.Append(revision, hlc.Timestamp{}); err != nil {
		return err
	}
	if err := j.checkRotationFault(rotationAfterCheckpoint); err != nil {
		return err
	}
	if err := writeNativeManifest(j.anchor, manifest, false); err != nil {
		return err
	}
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
	if err := j.checkRotationFault(rotationAfterCleanup); err != nil {
		return err
	}
	stage.Commit()
	return nil
}
