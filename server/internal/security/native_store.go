package security

import (
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
)

const (
	SystemRevisionKey       = "sys:security:revision"
	DefaultSystemJournalMax = int64(64 << 20)
	MaxSystemJournalBytes   = int64(512 << 20)
)

// NativeStoreOptions bind one reserved GraphCache image to a distinct security
// WAL, never a receipt epoch, graph HLC or ephemeral graph origin.
type NativeStoreOptions struct {
	Path            string
	Generation      [16]byte
	PublicKey       ed25519.PublicKey
	PrivateKey      ed25519.PrivateKey
	Limits          PolicyLimits
	Graph           *graphcache.GraphCache[string, *pb.Vertex]
	MaxJournalBytes int64
}

// NativeStore owns a synchronous typed WAL and its process lease. It is an
// inactive recovery component: production construction still needs audit,
// management transactions and serving-lease admission. Its manifest-selected
// signed checkpoints rotate the independent bounded control journal; no data
// RPC uses this synchronous durability lane.
type NativeStore struct {
	store   *Store
	journal *nativeJournal
}

func (n *NativeStore) Store() *Store { return n.store }

func (n *NativeStore) Close() error {
	if n == nil {
		return nil
	}
	n.store.faulted.Store(true)
	return n.journal.close()
}

// CreateNativeStore is explicit genesis. An existing or partially created WAL
// is never overwritten or automatically resumed. Discard the candidate graph
// after any construction error; no partially restored runtime is returned.
func CreateNativeStore(options NativeStoreOptions) (*NativeStore, error) {
	native, err := prepareNativeStore(options)
	if err != nil {
		return nil, err
	}

	lease, err := mutationlog.AcquireFileWALLease(native.journal.anchor)
	if err != nil {
		return nil, err
	}
	transferred := false
	defer func() {
		if !transferred {
			_ = lease.Close()
		}
	}()
	if err := requireFreshNativeFamily(native.journal.anchor); err != nil {
		return nil, err
	}
	log, owner, err := createNativeSegment(native.journal.anchor, native.journal.binding)
	if err != nil {
		return nil, err
	}
	if err := writeNativeManifest(native.journal.anchor, nativeManifest{binding: native.journal.binding}, true); err != nil {
		return nil, errors.Join(err, owner.Close())
	}
	if err := native.journal.attach(log, owner, native.journal.anchor); err != nil {
		return nil, errors.Join(err, owner.Close())
	}
	native.journal.lease = lease
	transferred = true

	return native, nil
}

// ResumeNativeStore verifies every signed contiguous revision and the durable
// lower-bound tip before installing the final complete GraphCache image. It
// refuses torn/corrupt/rolled-back histories; it never truncates or guesses.
// A valid unpublished suffix is recovered before the Store is returned.
func ResumeNativeStore(options NativeStoreOptions) (_ *NativeStore, err error) {
	native, err := prepareNativeStore(options)
	if err != nil {
		return nil, err
	}
	lease, err := mutationlog.AcquireFileWALLease(options.Path)
	if err != nil {
		return nil, err
	}
	var tip *mutationlog.FileWALTipJournal
	var owner io.Closer
	transferred := false
	defer func() {
		if !transferred {
			if owner != nil {
				err = errors.Join(err, owner.Close())
			}
			if tip != nil {
				err = errors.Join(err, tip.Close())
			}
			err = errors.Join(err, lease.Close())
		}
	}()
	err = lease.WithPath(func(anchor string) error {
		manifest, err := readNativeManifest(anchor, native.journal.binding)
		if err != nil {
			return err
		}
		native.journal.base = manifest.base
		path := manifest.path(anchor)
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return statErr
		}
		if !info.Mode().IsRegular() || info.Size() > native.journal.maxBytes {
			return ErrSystemJournalCapacity
		}
		tipInfo, statErr := os.Lstat(path + ".tip")
		if statErr != nil {
			return statErr
		}
		if !tipInfo.Mode().IsRegular() || tipInfo.Size() > native.journal.maxBytes-info.Size() {
			return ErrSystemJournalCapacity
		}
		decode := func(raw []byte) (mutationlog.MutationOp, error) {
			return DecodeRevision(raw, native.store.publicKey, options.Limits)
		}
		validate := func(entry mutationlog.Entry) error {
			revision, entryErr := native.store.systemEntry(entry)
			if entryErr == nil && entry.Seq == 1 && manifest.segment != [16]byte{} &&
				(revision.digest != manifest.checkpoint || !revision.completeCheckpointHistory()) {
				return ErrInvalidRevision
			}
			return entryErr
		}
		// Account for all tip records needed to attest an unpublished suffix
		// before catch-up writes anything to the sidecar.
		cut, openErr := mutationlog.InspectFileWALCut(path, 0, decode, validate)
		if openErr != nil {
			return openErr
		}
		if manifest.segment != [16]byte{} && cut.ObservedLast == 0 {
			return ErrInvalidRevision
		}
		fullBytes, openErr := systemJournalSize(cut.ObservedOffset, cut.ObservedLast)
		if openErr != nil || fullBytes+int64(systemManifestBytes) > native.journal.maxBytes {
			return ErrSystemJournalCapacity
		}
		tip, openErr = mutationlog.ResumeFileWALTipJournal(path, systemJournalBinding(options))
		if openErr != nil {
			return openErr
		}
		restore := func(entry mutationlog.Entry) error {
			revision, entryErr := native.store.systemEntry(entry)
			if entryErr != nil {
				return entryErr
			}
			if entry.Seq == 1 && manifest.segment != [16]byte{} {
				return native.store.restoreCheckpoint(revision)
			}
			return native.store.restoreNativeRevision(revision)
		}
		var log *mutationlog.Log
		log, owner, openErr = mutationlog.ResumeLogFromFileWALWithTip(path,
			mutationlog.Options{Capacity: 1}, encodeSystemRevision, decode, validate, restore, tip)
		if openErr != nil {
			return openErr
		}
		if openErr = native.journal.attach(log, owner, path); openErr != nil {
			return openErr
		}
		if err := cleanupNativeSegments(anchor, path); err != nil {
			return err
		}
		if revision := native.store.current.Load(); revision != nil {
			return native.journal.metadata.InstallRecovered(revision.sequence, revision.encoded)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Close in dependency order: Log/WAL, tip, then the process path lease.
	native.journal.tip, native.journal.lease = tip, lease
	transferred = true
	return native, nil
}

func prepareNativeStore(options NativeStoreOptions) (*NativeStore, error) {
	if !filepath.IsAbs(options.Path) || options.Graph == nil {
		return nil, ErrStoreUnavailable
	}
	maxBytes := options.MaxJournalBytes
	if maxBytes == 0 {
		maxBytes = DefaultSystemJournalMax
	}
	if maxBytes < 1024 || maxBytes > MaxSystemJournalBytes {
		return nil, ErrSystemJournalCapacity
	}
	directory, err := filepath.EvalSymlinks(filepath.Dir(options.Path))
	if err != nil {
		return nil, err
	}
	path := filepath.Join(directory, filepath.Base(options.Path))
	journal := &nativeJournal{maxBytes: maxBytes, path: path, anchor: path, binding: systemJournalBinding(options)}
	store, err := NewStore(StoreOptions{Generation: options.Generation, PublicKey: options.PublicKey,
		PrivateKey: options.PrivateKey, Limits: options.Limits, Committer: journal})
	if err != nil {
		return nil, err
	}
	journal.store = store
	journal.metadata, err = options.Graph.EnableSystemMetadata(SystemRevisionKey,
		maxRevisionBytes)
	if err != nil {
		return nil, err
	}
	return &NativeStore{store: store, journal: journal}, nil
}

func systemJournalBinding(options NativeStoreOptions) [32]byte {
	data := []byte("lantern-system-journal-v1\x00" + keyspace.Version + "\x00")
	data = append(data, options.Generation[:]...)
	data = append(data, options.PublicKey...)
	return sha256.Sum256(data)
}

func encodeSystemRevision(op mutationlog.MutationOp) ([]byte, error) {
	revision, ok := op.(*Revision)
	if !ok || revision == nil {
		return nil, ErrInvalidRevision
	}
	return revision.Encode(), nil
}

func (s *Store) systemEntry(entry mutationlog.Entry) (*Revision, error) {
	revision, ok := entry.Op.(*Revision)
	journal, journalOK := s.committer.(*nativeJournal)
	if !journalOK || entry.Seq > ^uint64(0)-journal.base {
		return nil, ErrInvalidRevision
	}
	if !ok || revision == nil || entry.HLC != (hlc.Timestamp{}) || entry.Seq+journal.base != revision.sequence ||
		revision.generation != s.generation {
		return nil, ErrInvalidRevision
	}
	return revision, nil
}

func (s *Store) restoreNativeRevision(revision *Revision) error {
	current := s.current.Load()
	if current == nil {
		if revision.sequence != 1 || revision.previous != [32]byte{} || revision.snapshot.Image().BootstrapRevision == 0 {
			return ErrRevisionConflict
		}
	} else if revision.sequence != current.sequence+1 || revision.previous != current.digest ||
		revision.snapshot.Image().BootstrapRevision < current.snapshot.Image().BootstrapRevision {
		return ErrRevisionConflict
	}
	if current != nil && revision.snapshot.Image().BootstrapRevision == current.snapshot.Image().BootstrapRevision &&
		!sameEnvOwned(current.snapshot.Image(), revision.snapshot.Image()) {
		return ErrBootstrapLocked
	}
	if _, known := s.changes[revision.changeID]; known {
		return ErrChangeConflict
	}
	s.publish(revision)
	return nil
}

// Compile-time boundary: this component is a control committer, never a graph
// RPC WAL or receipt-origin adapter.
var _ RevisionCommitter = (*nativeJournal)(nil)
