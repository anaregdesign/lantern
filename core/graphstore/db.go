// Package graphstore stores generation-scoped graph rows and private service
// records in a single node-owned Tarp database.
package graphstore

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

var (
	ErrInvalidManifest = errors.New("graphstore: invalid storage manifest")
	ErrNodeMismatch    = errors.New("graphstore: database belongs to a different node")
)

const manifestKey = "H"
const manifestFormat = "LGS2"

// The 32-MiB relay frame plus row framing must fit one record. Leave space
// for related graph and service records in the same atomic write. The B+tree
// and value cache keep their separate bounded working sets.
func storageOptions() tarp.Options {
	return tarp.Options{MaxRecordBytes: 33 << 20, TargetSegmentBytes: 128 << 20}
}

type manifest struct {
	node      hlc.NodeID
	active    uint64
	allocated uint64
	revision  uint64 // active application commits; staging/cleanup do not advance it
}

func (m manifest) encode() []byte {
	b := append([]byte(manifestFormat), m.node[:]...)
	b = binary.BigEndian.AppendUint64(b, m.active)
	b = binary.BigEndian.AppendUint64(b, m.allocated)
	return binary.BigEndian.AppendUint64(b, m.revision)
}

func readManifest(get func([]byte) ([]byte, error)) (manifest, error) {
	b, err := get([]byte(manifestKey))
	if err != nil {
		return manifest{}, err
	}
	if len(b) != 44 || string(b[:4]) != manifestFormat {
		return manifest{}, ErrInvalidManifest
	}
	m := manifest{node: hlc.NodeID(b[4:20]), active: binary.BigEndian.Uint64(b[20:28]), allocated: binary.BigEndian.Uint64(b[28:36]), revision: binary.BigEndian.Uint64(b[36:])}
	if m.node == (hlc.NodeID{}) || m.active == 0 || m.active > m.allocated || (m.revision == 0 && m.active != 1) {
		return manifest{}, ErrInvalidManifest
	}
	return m, nil
}

func (m *manifest) advance() error {
	if m.revision == math.MaxUint64 {
		return errors.New("graphstore: exhausted application revision")
	}
	m.revision++
	return nil
}

// DB owns one private Tarp directory. All changes, including generation
// install, share its writer gate. Close the DB once the serving runtime stops.
type DB struct {
	mu            sync.Mutex
	raw           *tarp.DB
	node          hlc.NodeID
	head          manifest
	staging       *Generation
	pinMu         sync.Mutex
	pins          map[uint64]int
	retiredPins   map[uint64]bool
	reclaimSignal chan struct{}
	cancelReclaim context.CancelFunc
	reclaimDone   chan struct{}
	closeOnce     sync.Once
	fault         atomic.Pointer[admissionFailure]
	closed        bool
	closeErr      error
}

// Open creates or reopens a node database. An explicit expected node ID is
// checked on every open. With a zero expected ID a fresh DB gets a random
// stable ID, and an existing DB retains its stored one.
func Open(ctx context.Context, dir string, expected hlc.NodeID) (*DB, error) {
	raw, err := tarp.OpenContext(ctx, dir, storageOptions())
	if err != nil {
		return nil, err
	}
	db, err := openDB(raw, expected, true)
	if err != nil {
		return nil, errors.Join(err, raw.Close())
	}
	return db, nil
}

// OpenExisting requires an initialized Lantern graph store. It never creates
// a directory, publishes a manifest, or chooses a new node ID.
func OpenExisting(ctx context.Context, dir string, expected hlc.NodeID) (*DB, error) {
	raw, err := tarp.OpenExisting(ctx, dir, storageOptions())
	if err != nil {
		return nil, err
	}
	db, err := openDB(raw, expected, false)
	if err != nil {
		return nil, errors.Join(err, raw.Close())
	}
	return db, nil
}

func openDB(raw *tarp.DB, expected hlc.NodeID, initialize bool) (*DB, error) {
	snap, err := raw.Snapshot()
	if err != nil {
		return nil, err
	}
	defer snap.Close()
	head, err := readManifest(snap.Get)
	if errors.Is(err, tarp.ErrNotFound) {
		// A missing manifest in a database with any committed records is
		// corruption, even if every other key has since been deleted.
		if !initialize || snap.Sequence() != 0 {
			return nil, ErrInvalidManifest
		}
		node := expected
		if node == (hlc.NodeID{}) {
			for node == (hlc.NodeID{}) {
				rand.Read(node[:])
			}
		}
		head = manifest{node: node, active: 1, allocated: 1}
		var batch tarp.WriteBatch
		batch.Put([]byte(manifestKey), head.encode())
		batch.Put(generationMarker(1), nil)
		if err := raw.Apply(&batch); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	} else {
		marker, err := snap.Get(generationMarker(head.active))
		if errors.Is(err, tarp.ErrNotFound) || (err == nil && len(marker) != 0) {
			return nil, ErrInvalidManifest
		}
		if err != nil {
			return nil, err
		}
	}
	if expected != (hlc.NodeID{}) && head.node != expected {
		return nil, ErrNodeMismatch
	}
	return &DB{raw: raw, node: head.node, head: head, pins: make(map[uint64]int), retiredPins: make(map[uint64]bool), reclaimSignal: make(chan struct{}, 1)}, nil
}

// NodeID returns the persistent identity bound to this directory.
func (db *DB) NodeID() hlc.NodeID { return db.node }

type admissionFailure struct{ err error }

func (db *DB) available() error {
	if failure := db.fault.Load(); failure != nil {
		return failure.err
	}
	return nil
}

func (db *DB) apply(batch *tarp.WriteBatch) error {
	if err := db.available(); err != nil {
		return err
	}
	return db.observeWrite(db.raw.Apply(batch))
}

func (db *DB) observeWrite(err error) error {
	if err != nil {
		var op *tarp.OpError
		if errors.Is(err, tarp.ErrUnhealthy) || (errors.As(err, &op) && op.Outcome != tarp.NotApplied) {
			// Only the failing request owns this outcome. Later calls
			// observe the fence without inheriting its commit result.
			db.fault.CompareAndSwap(nil, &admissionFailure{fmt.Errorf("%w: prior operation requires recovery: %v", tarp.ErrUnhealthy, err)})
		}
	}
	return err
}

func (db *DB) commit(next manifest, batch *tarp.WriteBatch) error {
	batch.Put([]byte(manifestKey), next.encode())
	if err := db.apply(batch); err != nil {
		return err
	}
	db.head = next
	return nil
}

// Update reads and prepares an active mutation under the writer gate. Queue
// generation-scoped graph and service rows only. Returning an error discards
// every queued operation; a committed or unknown Apply failure remains an
// inspectable *tarp.OpError. The callback must not retain its borrowed inputs.
func (db *DB) Update(prepare func(generation uint64, snapshot *tarp.Snapshot, batch *tarp.WriteBatch) error) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.available(); err != nil {
		return err
	}
	snap, err := db.raw.Snapshot()
	if err != nil {
		return err
	}
	defer snap.Close()
	var batch tarp.WriteBatch
	if err := prepare(db.head.active, snap, &batch); err != nil {
		return err
	}
	if batch.Len() == 0 {
		return nil
	}
	next := db.head
	if err := next.advance(); err != nil {
		return err
	}
	return db.commit(next, &batch)
}

// ReadView pins the manifest and rows from a single database snapshot.
// Revision counts active application commits, starting at zero for an empty
// database. Always call ReadView.Close after the final read or scan.
type ReadView struct {
	Generation uint64
	Revision   uint64
	Snapshot   *tarp.Snapshot
	db         *DB
	generation uint64
	closeOnce  sync.Once
	closeErr   error
}

// Database returns the node database that owns this view.
func (v *ReadView) Database() *DB { return v.db }

// Read obtains an active read cut while generation switches and writers run.
func (db *DB) Read() (*ReadView, error) {
	db.pinMu.Lock()
	defer db.pinMu.Unlock()
	if err := db.available(); err != nil {
		return nil, err
	}
	snap, err := db.raw.Snapshot()
	if err != nil {
		return nil, err
	}
	cut, err := readManifest(snap.Get)
	if err != nil {
		snap.Close()
		return nil, err
	}
	if cut.node != db.node {
		snap.Close()
		db.fault.CompareAndSwap(nil, &admissionFailure{ErrInvalidManifest})
		return nil, ErrInvalidManifest
	}
	db.pins[cut.active]++
	return &ReadView{Generation: cut.active, Revision: cut.revision, Snapshot: snap, db: db, generation: cut.active}, nil
}

// WithCommittedSnapshot borrows the atomically selected generation and rows.
// It never takes the writer gate and can run while a writer prepares changes.
func (db *DB) WithCommittedSnapshot(read func(uint64, *tarp.Snapshot) error) error {
	view, err := db.Read()
	if err != nil {
		return err
	}
	defer view.Close()
	return read(view.Generation, view.Snapshot)
}

// Close releases the Tarp read view and its generation reclamation pin.
func (v *ReadView) Close() error {
	v.closeOnce.Do(func() {
		v.closeErr = v.Snapshot.Close()
		v.db.pinMu.Lock()
		defer v.db.pinMu.Unlock()
		v.db.pins[v.generation]--
		if v.db.pins[v.generation] == 0 {
			delete(v.db.pins, v.generation)
			if v.db.retiredPins[v.generation] {
				delete(v.db.retiredPins, v.generation)
				v.db.requestReclamation()
			}
		}
	})
	return v.closeErr
}

// Close stops admission and releases the Tarp directory.
func (db *DB) Close() error {
	db.closeOnce.Do(func() {
		db.mu.Lock()
		db.closed = true
		db.fault.Store(&admissionFailure{tarp.ErrClosed})
		done := db.reclaimDone
		if db.cancelReclaim != nil {
			db.cancelReclaim()
		}
		db.mu.Unlock()
		if done != nil {
			<-done
		}
		db.closeErr = db.raw.Close()
	})
	return db.closeErr
}
