package security

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
)

var ErrSystemJournalCapacity = errors.New("system journal capacity exhausted")

const (
	systemTipHeaderBytes = 9 + 32 + 32 + 4
	systemFrameBytes     = 8 + 36
	systemTipRecordBytes = 8 + 32 + 4
)

func systemJournalSize(offset int64, sequence uint64) (int64, error) {
	if offset < 8 || offset > MaxSystemJournalBytes || sequence > uint64(MaxSystemJournalBytes/systemTipRecordBytes) {
		return 0, ErrSystemJournalCapacity
	}
	return offset + systemTipHeaderBytes + int64(sequence)*systemTipRecordBytes, nil
}

// nativeJournal is private to NativeStore. Its bounded retained Log holds only
// one revision. Signed checkpoints and a synced selector bound the on-disk
// history while preserving original authority and lower-bound tip proofs.
type nativeJournal struct {
	mu            sync.Mutex
	metadata      *graphcache.SystemMetadata
	log           *mutationlog.Log
	provenance    *mutationlog.FileWALTipProvenance
	owner         io.Closer
	tip           *mutationlog.FileWALTipJournal
	lease         *mutationlog.FileWALLease
	path          string
	anchor        string
	binding       [32]byte
	base          uint64
	store         *Store
	rotationFault func(rotationFaultPoint) error
	maxBytes      int64
	closed        bool
}

func (j *nativeJournal) attach(log *mutationlog.Log, owner io.Closer, path string) error {
	provenance, err := log.FileWALTipProvenance(path)
	if err != nil {
		return err
	}
	j.log, j.owner, j.path, j.provenance = log, owner, path, provenance
	return nil
}

func (j *nativeJournal) CommitRevision(ctx context.Context, revision *Revision) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed || j.log == nil || revision == nil {
		return ErrStoreUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	witness, err := j.provenance.TipWitness(j.path)
	if err != nil {
		return err
	}
	// The existing FileWAL frame adds 8 length/CRC + 36 sequence/HLC bytes.
	// Its tip adds one 44-byte lower-bound record per synced revision. Count
	// both files, including the fixed header, in one bounded control budget.
	used, err := systemJournalSize(witness.Offset, witness.Seq)
	if err != nil {
		return err
	}
	if witness.Seq+j.base != revision.sequence-1 {
		return ErrInvalidRevision
	}
	used += int64(systemManifestBytes)
	nextBytes := int64(len(revision.encoded)) + systemFrameBytes + systemTipRecordBytes
	if used+nextBytes > j.maxBytes/2 && witness.Seq != 0 {
		if err := j.rotate(ctx, used, nextBytes); err != nil {
			return err
		}
		witness, err = j.provenance.TipWitness(j.path)
		if err != nil {
			return err
		}
		used, err = systemJournalSize(witness.Offset, witness.Seq)
		if err != nil {
			return err
		}
		used += int64(systemManifestBytes)
	}
	if nextBytes > j.maxBytes-used {
		return ErrSystemJournalCapacity
	}
	stage, err := j.metadata.Prepare(revision.previous, revision.sequence, revision.encoded)
	if err != nil {
		return err
	}
	defer stage.Abort()
	_, err = j.log.CommitWithPublication(revision, hlc.Timestamp{}, func(mutationlog.Entry) { stage.Commit() })
	return err
}

func (j *nativeJournal) close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil
	}
	j.closed = true
	if j.owner == nil {
		return nil
	}
	err := j.owner.Close()
	if j.tip != nil {
		err = errors.Join(err, j.tip.Close())
	}
	if j.lease != nil {
		err = errors.Join(err, j.lease.Close())
	}
	return err
}
