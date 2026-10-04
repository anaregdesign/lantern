package security

import (
	"crypto/ed25519"
	"encoding/binary"
	"math"
)

// A complete signed revision retains enough original transaction evidence to
// recover the bounded change-ID window from a single authenticated checkpoint.
// Replica-local publication never changes these authority-signed records.
type checkpointChange struct {
	id     [16]byte
	record changeRecord
}

const changeRecordBytes = 16 + 8 + 32 + 8 + 32
const maxRevisionBytes = revisionHeaderBytes + MaxImageBytes + (retainedChanges-1)*changeRecordBytes + ed25519.SignatureSize

func (s *Store) checkpointHistory() []checkpointChange {
	count := min(len(s.order), retainedChanges-1)
	history := make([]checkpointChange, count)
	for i, id := range s.order[len(s.order)-count:] {
		history[i] = checkpointChange{id: id, record: s.changes[id]}
	}
	return history
}

func validateChangeHistory(history []checkpointChange, sequence uint64, currentID [16]byte) error {
	if len(history) > retainedChanges-1 || uint64(len(history)) >= sequence {
		return ErrInvalidRevision
	}
	seen := make(map[[16]byte]bool, len(history))
	for i, item := range history {
		result := item.record.result
		expectedSequence := sequence - uint64(len(history)) + uint64(i)
		if item.id == [16]byte{} || item.id == currentID || seen[item.id] ||
			result.Revision == 0 || result.Revision == math.MaxUint64 || result.Revision != expectedSequence ||
			item.record.expected != result.Revision-1 || item.record.intentDigest == [32]byte{} || result.Digest == [32]byte{} {
			return ErrInvalidRevision
		}
		seen[item.id] = true
	}
	return nil
}

func appendChangeHistory(raw []byte, history []checkpointChange) []byte {
	for _, item := range history {
		raw = append(raw, item.id[:]...)
		raw = binary.BigEndian.AppendUint64(raw, item.record.expected)
		raw = append(raw, item.record.intentDigest[:]...)
		raw = binary.BigEndian.AppendUint64(raw, item.record.result.Revision)
		raw = append(raw, item.record.result.Digest[:]...)
	}
	return raw
}

func decodeChangeHistory(raw []byte, count int) []checkpointChange {
	history := make([]checkpointChange, count)
	for i := range history {
		item := &history[i]
		copy(item.id[:], raw[:16])
		item.record.expected = binary.BigEndian.Uint64(raw[16:24])
		copy(item.record.intentDigest[:], raw[24:56])
		item.record.result.Revision = binary.BigEndian.Uint64(raw[56:64])
		copy(item.record.result.Digest[:], raw[64:96])
		raw = raw[changeRecordBytes:]
	}
	return history
}

func (r *Revision) completeCheckpointHistory() bool {
	return len(r.history) == int(min(r.sequence-1, uint64(retainedChanges-1)))
}

// restoreCheckpoint is called only for the manifest-selected first WAL frame,
// after its original signature and complete retained change window were proved.
func (s *Store) restoreCheckpoint(revision *Revision) error {
	if revision == nil || s.current.Load() != nil || revision.generation != s.generation ||
		!revision.completeCheckpointHistory() || revision.snapshot.Image().BootstrapRevision == 0 {
		return ErrRevisionConflict
	}
	for _, item := range revision.history {
		s.changes[item.id] = item.record
		s.order = append(s.order, item.id)
	}
	s.publish(revision)
	return nil
}
