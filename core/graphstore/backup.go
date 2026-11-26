package graphstore

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"

	"github.com/anaregdesign/tarp"
)

// ErrBackupCutChanged means an intervening local commit separated the
// application read snapshot from Tarp's backup capture. The caller owns
// the valid but unbound destination and must not publish it as this cut.
var ErrBackupCutChanged = errors.New("graphstore: backup cut changed")

// Backup copies this node's native Tarp image into a new destination. Bind
// reads the independently pinned source view only after the complete native
// backup was verified at the same Tarp sequence. Normally writes proceed
// throughout the copy. After ErrBackupCutChanged a caller may retry once into
// another new destination with serialize=true, fencing writers for the copy.
func (db *DB) Backup(ctx context.Context, destination string, serialize bool, bind func(generation, revision uint64, s *tarp.Snapshot) error) (tarp.BackupInfo, error) {
	if err := ctx.Err(); err != nil {
		return tarp.BackupInfo{}, err
	}
	db.mu.Lock()
	if err := db.available(); err != nil {
		db.mu.Unlock()
		return tarp.BackupInfo{}, err
	}
	db.interruptKeyOrder()
	view, err := db.Read()
	if err != nil {
		db.mu.Unlock()
		return tarp.BackupInfo{}, err
	}
	defer view.Close()
	if !serialize {
		db.mu.Unlock()
	}
	err = db.raw.Backup(ctx, destination)
	if serialize {
		db.mu.Unlock()
	}
	if err != nil {
		return tarp.BackupInfo{}, err
	}
	info, err := tarp.VerifyBackup(ctx, destination)
	if err != nil {
		return tarp.BackupInfo{}, err
	}
	if info.LastUserSequence != view.Snapshot.Sequence() {
		return tarp.BackupInfo{}, ErrBackupCutChanged
	}
	if err := bind(view.Generation, view.Revision, view.Snapshot); err != nil {
		return tarp.BackupInfo{}, err
	}
	return info, nil
}

// Restore copies and verifies an immutable native image to a new directory.
// Call OpenExisting and verify the application cut before selecting the image.
func Restore(ctx context.Context, source, destination string) error {
	return tarp.Restore(ctx, tarp.RestoreRequest{Source: source, Destination: destination, Limits: storageOptions().RestoreLimits()})
}

// StateDigest binds all authoritative graph rows, causal metadata, and private
// service rows in the selected generation. Length framing and ordered keys
// make the streaming digest independent of storage layout and compaction.
func StateDigest(ctx context.Context, generation uint64, snap *tarp.Snapshot) ([32]byte, error) {
	h := sha256.New()
	h.Write([]byte("Lantern graph state v1"))
	var lengths [16]byte
	for _, domain := range []byte{'G', 'S'} {
		prefix := binary.BigEndian.AppendUint64([]byte{domain}, generation)
		lo, hi := tarp.PrefixRange(prefix)
		for row, err := range snap.ScanRange(ctx, lo, hi) {
			if err != nil {
				return [32]byte{}, err
			}
			if len(row.Key) < 10 {
				return [32]byte{}, ErrInvalidManifest
			}
			binary.BigEndian.PutUint64(lengths[:], uint64(len(row.Key)))
			binary.BigEndian.PutUint64(lengths[8:], uint64(len(row.Value)))
			h.Write(lengths[:])
			h.Write(row.Key)
			h.Write(row.Value)
		}
	}
	return [32]byte(h.Sum(nil)), ctx.Err()
}
