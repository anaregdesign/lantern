package graphcache

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"math"
	"sync"
)

const MaxSystemMetadataBytes = 8 << 20

var (
	ErrSystemMetadataInvalid  = errors.New("graphcache: invalid system metadata image")
	ErrSystemMetadataConflict = errors.New("graphcache: system metadata revision conflict")
)

// SystemMetadata retains one complete, bounded image outside graph TTL,
// eviction, topology and search statistics. A typed owner validates the image
// before staging and persists it before committing; generic graph replication
// cannot install it. Never export this handle through a public data API.
type SystemMetadata struct {
	mu       sync.RWMutex
	key      string
	maxBytes int
	current  SystemMetadataRecord
}

// SystemMetadataRecord owns its bytes when returned by Snapshot. Digest covers
// the exact Value bytes, rather than a graph HLC or transport-local sequence.
type SystemMetadataRecord struct {
	Revision uint64
	Digest   [32]byte
	Value    []byte
}

func (m *SystemMetadata) Key() string { return m.key }

func (m *SystemMetadata) Snapshot() SystemMetadataRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return SystemMetadataRecord{Revision: m.current.Revision, Digest: m.current.Digest,
		Value: bytes.Clone(m.current.Value)}
}

// InstallRecovered initializes an empty reserved image after the owner has
// validated its complete durable history. It never replaces a live image and
// does not certify signature, recovery provenance or serving freshness.
func (m *SystemMetadata) InstallRecovered(revision uint64, value []byte) error {
	if m == nil || revision == 0 || revision == math.MaxUint64 || len(value) == 0 || len(value) > m.maxBytes {
		return ErrSystemMetadataInvalid
	}
	owned := bytes.Clone(value)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current.Revision != 0 {
		return ErrSystemMetadataConflict
	}
	m.current = SystemMetadataRecord{Revision: revision, Value: owned, Digest: sha256.Sum256(owned)}
	return nil
}

// Prepare reserves the next image against its exact prior digest. Its stage
// blocks system readers until Commit or Abort, without holding any graph lock.
// The caller must resolve the stage on every path, including I/O failures.
func (m *SystemMetadata) Prepare(expected [32]byte, revision uint64, value []byte) (*SystemMetadataStage, error) {
	return m.prepare(expected, revision, value, false)
}

// PrepareCheckpoint is exclusively for a typed owner that has authenticated a
// complete newer checkpoint and a fresh serving-authority proof. It permits a
// revision gap, but never rollback or replacement without the exact prior
// digest. It does not itself verify that proof or authorize public sys: CRUD.
func (m *SystemMetadata) PrepareCheckpoint(expected [32]byte, revision uint64, value []byte) (*SystemMetadataStage, error) {
	return m.prepare(expected, revision, value, true)
}

func (m *SystemMetadata) prepare(expected [32]byte, revision uint64, value []byte, checkpoint bool) (*SystemMetadataStage, error) {
	if m == nil || revision == 0 || revision == math.MaxUint64 || len(value) == 0 || len(value) > m.maxBytes {
		return nil, ErrSystemMetadataInvalid
	}
	owned := bytes.Clone(value)
	record := SystemMetadataRecord{Revision: revision, Value: owned, Digest: sha256.Sum256(owned)}
	m.mu.Lock()
	if expected != m.current.Digest || revision <= m.current.Revision || !checkpoint && revision != m.current.Revision+1 {
		m.mu.Unlock()
		return nil, ErrSystemMetadataConflict
	}
	return &SystemMetadataStage{metadata: m, next: record}, nil
}
