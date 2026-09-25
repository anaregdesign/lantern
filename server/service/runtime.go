package service

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// ServingRuntime is the single process-owned state cut installed before any
// listener or replication worker is constructed. Its receipt state remains
// private: constructing a durable runtime does not enable receipt capability,
// status, or receipt-bearing mutation RPCs.
type ServingRuntime struct {
	graph    *graphcache.GraphCache[string, *pb.Vertex]
	log      *mutationlog.Log
	clock    *hlc.Clock
	origins  *originStateTracker
	receipt  *receiptServingRuntime
	owner    io.Closer
	close    sync.Once
	closeErr error
}

type receiptServingRuntime struct {
	store      *mutationreceipt.Store
	policy     mutationreceipt.Config
	epoch      mutationreceipt.Epoch
	generation [16]byte
}

const (
	receiptRuntimeGenerationMagic = "LNRGEN1\n"
	receiptRuntimeGenerationSize  = len(receiptRuntimeGenerationMagic) + sha256.Size + 16 + sha256.Size
)

// DurableReceiptWALRuntimeConfig is the validated input for a fresh or
// same-epoch restart. The caller supplies the production graph policy before
// replay; all durable ownership remains inside the returned ServingRuntime.
type DurableReceiptWALRuntimeConfig struct {
	Path           string
	Receipt        mutationreceipt.Config
	Log            mutationlog.Options
	DefaultTTL     time.Duration
	ConfigureGraph func(*graphcache.GraphCache[string, *pb.Vertex]) error
	NodeID         hlc.NodeID
	Now            time.Time
}

// NewGraphOnlyServingRuntime preserves the historical in-memory composition.
// The runtime owns log and closes it after all serving goroutines stop.
func NewGraphOnlyServingRuntime(
	graph *graphcache.GraphCache[string, *pb.Vertex],
	log *mutationlog.Log,
	clock *hlc.Clock,
) (*ServingRuntime, error) {
	if graph == nil || log == nil || clock == nil {
		return nil, errors.New("service: graph-only runtime requires graph, log, and clock")
	}
	return &ServingRuntime{
		graph: graph, log: log, clock: clock, origins: newOriginStateTracker(), owner: log,
	}, nil
}

// CreateDurableReceiptWALServingRuntime creates a new genesis WAL generation.
// Existing or partially-created files are rejected by the owned candidate.
func CreateDurableReceiptWALServingRuntime(config DurableReceiptWALRuntimeConfig) (*ServingRuntime, error) {
	if err := validateDurableReceiptWALRuntimeConfig(config); err != nil {
		return nil, err
	}
	candidate, err := createLeasedReceiptWALCandidate(
		config.Path,
		config.Receipt,
		config.Log,
		config.DefaultTTL,
		config.ConfigureGraph,
	)
	if err != nil {
		return nil, err
	}
	generation, err := createReceiptRuntimeGeneration(
		candidate.lease.Path(),
		config.Receipt.Epoch,
		candidate.state.receipts.PolicyFingerprint(),
		config.NodeID,
	)
	if err != nil {
		return nil, errors.Join(err, candidate.Close())
	}
	return certifyReceiptWALServingRuntime(candidate, config.NodeID, generation, config.Receipt)
}

// OpenDurableReceiptWALServingRuntime resumes one complete genesis WAL under
// its lease. Missing, corrupt, mismatched, truncated, or unreplayable state is
// rejected without rotating or reinitializing any durable bytes.
func OpenDurableReceiptWALServingRuntime(config DurableReceiptWALRuntimeConfig) (*ServingRuntime, error) {
	if err := validateDurableReceiptWALRuntimeConfig(config); err != nil {
		return nil, err
	}
	now := config.Now
	if now.IsZero() {
		now = time.Now()
	}
	var generation [16]byte
	candidate, err := openLeasedReceiptWALCandidate(
		config.Path,
		config.Receipt,
		now,
		config.Log,
		config.DefaultTTL,
		config.ConfigureGraph,
		func(canonicalPath string, policy [sha256.Size]byte) error {
			var readErr error
			generation, readErr = readReceiptRuntimeGeneration(
				canonicalPath,
				config.Receipt.Epoch,
				policy,
				config.NodeID,
			)
			return readErr
		},
	)
	if err != nil {
		return nil, err
	}
	return certifyReceiptWALServingRuntime(candidate, config.NodeID, generation, config.Receipt)
}

func validateDurableReceiptWALRuntimeConfig(config DurableReceiptWALRuntimeConfig) error {
	if config.Path == "" || !filepath.IsAbs(config.Path) ||
		config.ConfigureGraph == nil || config.Log.WAL != nil {
		return errors.New("service: durable receipt WAL runtime requires an absolute path, graph policy, and no preconfigured WAL")
	}
	if config.NodeID == (hlc.NodeID{}) {
		return errors.New("service: durable receipt WAL runtime requires a nonzero node ID")
	}
	if _, err := mutationreceipt.New(config.Receipt); err != nil {
		return fmt.Errorf("service: durable receipt WAL policy: %w", err)
	}
	return nil
}

func newReceiptRuntimeGeneration() ([16]byte, error) {
	var generation [16]byte
	if _, err := io.ReadFull(rand.Reader, generation[:]); err != nil {
		return generation, fmt.Errorf("service: receipt runtime generation: %w", err)
	}
	if generation == ([16]byte{}) {
		return generation, errors.New("service: receipt runtime generation is zero")
	}
	return generation, nil
}

func createReceiptRuntimeGeneration(
	walPath string,
	epoch mutationreceipt.Epoch,
	policy [sha256.Size]byte,
	nodeID hlc.NodeID,
) (_ [16]byte, err error) {
	generation, err := newReceiptRuntimeGeneration()
	if err != nil {
		return generation, err
	}
	path := walPath + ".generation"
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return generation, fmt.Errorf("service: create receipt runtime generation: %w", err)
	}
	defer func() {
		err = errors.Join(err, file.Close())
	}()
	record := encodeReceiptRuntimeGeneration(walPath, epoch, policy, nodeID, generation)
	if _, err := file.Write(record); err != nil {
		return generation, fmt.Errorf("service: write receipt runtime generation: %w", err)
	}
	if err := file.Sync(); err != nil {
		return generation, fmt.Errorf("service: sync receipt runtime generation: %w", err)
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return generation, fmt.Errorf("service: open receipt runtime generation directory: %w", err)
	}
	if err := dir.Sync(); err != nil {
		_ = dir.Close()
		return generation, fmt.Errorf("service: sync receipt runtime generation directory: %w", err)
	}
	if err := dir.Close(); err != nil {
		return generation, fmt.Errorf("service: close receipt runtime generation directory: %w", err)
	}
	return generation, nil
}

func readReceiptRuntimeGeneration(
	walPath string,
	epoch mutationreceipt.Epoch,
	policy [sha256.Size]byte,
	nodeID hlc.NodeID,
) ([16]byte, error) {
	var generation [16]byte
	path := walPath + ".generation"
	info, err := os.Lstat(path)
	if err != nil {
		return generation, fmt.Errorf("service: stat receipt runtime generation: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() != int64(receiptRuntimeGenerationSize) {
		return generation, errors.New("service: receipt runtime generation is corrupt")
	}
	file, err := os.Open(path)
	if err != nil {
		return generation, fmt.Errorf("service: open receipt runtime generation: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return generation, fmt.Errorf("service: stat receipt runtime generation: %w", err)
	}
	if !os.SameFile(info, opened) {
		return generation, errors.New("service: receipt runtime generation changed during open")
	}
	record := make([]byte, receiptRuntimeGenerationSize)
	if _, err := io.ReadFull(file, record); err != nil {
		return generation, fmt.Errorf("service: read receipt runtime generation: %w", err)
	}
	final, err := os.Lstat(path)
	if err != nil {
		return generation, fmt.Errorf("service: restat receipt runtime generation: %w", err)
	}
	if !os.SameFile(info, final) {
		return generation, errors.New("service: receipt runtime generation changed during read")
	}
	wantBinding := receiptRuntimeGenerationBinding(walPath, epoch, policy, nodeID)
	if !bytes.Equal(record[:len(receiptRuntimeGenerationMagic)], []byte(receiptRuntimeGenerationMagic)) ||
		!bytes.Equal(record[len(receiptRuntimeGenerationMagic):len(receiptRuntimeGenerationMagic)+sha256.Size], wantBinding[:]) {
		return generation, errors.New("service: receipt runtime generation binding mismatch")
	}
	checksumStart := receiptRuntimeGenerationSize - sha256.Size
	checksum := sha256.Sum256(record[:checksumStart])
	if !bytes.Equal(record[checksumStart:], checksum[:]) {
		return generation, errors.New("service: receipt runtime generation checksum mismatch")
	}
	copy(generation[:], record[len(receiptRuntimeGenerationMagic)+sha256.Size:checksumStart])
	if generation == ([16]byte{}) {
		return generation, errors.New("service: receipt runtime generation is zero")
	}
	return generation, nil
}

func encodeReceiptRuntimeGeneration(
	walPath string,
	epoch mutationreceipt.Epoch,
	policy [sha256.Size]byte,
	nodeID hlc.NodeID,
	generation [16]byte,
) []byte {
	record := make([]byte, 0, receiptRuntimeGenerationSize)
	record = append(record, receiptRuntimeGenerationMagic...)
	binding := receiptRuntimeGenerationBinding(walPath, epoch, policy, nodeID)
	record = append(record, binding[:]...)
	record = append(record, generation[:]...)
	checksum := sha256.Sum256(record)
	return append(record, checksum[:]...)
}

func receiptRuntimeGenerationBinding(
	walPath string,
	epoch mutationreceipt.Epoch,
	policy [sha256.Size]byte,
	nodeID hlc.NodeID,
) [sha256.Size]byte {
	h := sha256.New()
	_, _ = h.Write([]byte("lantern-receipt-runtime-generation-v1\x00"))
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(walPath)))
	_, _ = h.Write(length[:])
	_, _ = h.Write([]byte(walPath))
	_, _ = h.Write(epoch[:])
	_, _ = h.Write(policy[:])
	_, _ = h.Write(nodeID[:])
	var binding [sha256.Size]byte
	copy(binding[:], h.Sum(nil))
	return binding
}

func certifyReceiptWALServingRuntime(
	candidate *receiptWALOwnedCandidate,
	nodeID hlc.NodeID,
	generation [16]byte,
	policy mutationreceipt.Config,
) (_ *ServingRuntime, err error) {
	if candidate == nil || candidate.state == nil || candidate.state.graph == nil ||
		candidate.state.receipts == nil || candidate.state.origins == nil ||
		candidate.state.log == nil || generation == ([16]byte{}) {
		if candidate != nil {
			_ = candidate.Close()
		}
		return nil, errors.New("service: durable receipt WAL candidate is incomplete")
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, candidate.Close())
		}
	}()
	policy.ClockHighWater = time.Time{}
	policyStore, err := mutationreceipt.New(policy)
	if err != nil {
		return nil, fmt.Errorf("service: certify receipt Snapshot policy: %w", err)
	}
	if candidate.state.receipts.Epoch() != policy.Epoch ||
		candidate.state.receipts.PolicyFingerprint() != policyStore.PolicyFingerprint() {
		return nil, errors.New("service: durable receipt WAL policy does not match recovered Store")
	}

	clock := hlc.New(nodeID, hlc.Options{})
	floor := candidate.state.hlcFrontier
	highWaterMillis := candidate.state.receipts.Stats().HighWaterMillis
	if highWaterMillis < 0 || highWaterMillis > math.MaxInt64/int64(time.Millisecond) {
		return nil, errors.New("service: durable receipt WAL clock high-water is outside HLC range")
	}
	if highWaterMillis > 0 {
		highWaterFloor := hlc.Timestamp{WallNs: highWaterMillis * int64(time.Millisecond)}
		if floor.Less(highWaterFloor) {
			floor = highWaterFloor
		}
	}
	if err := clock.RestoreFloor(floor); err != nil {
		return nil, fmt.Errorf("service: restore durable receipt WAL HLC frontier: %w", err)
	}

	return &ServingRuntime{
		graph:   candidate.state.graph,
		log:     candidate.state.log,
		clock:   clock,
		origins: candidate.state.origins,
		receipt: &receiptServingRuntime{
			store:      candidate.state.receipts,
			policy:     policy,
			epoch:      candidate.state.receipts.Epoch(),
			generation: generation,
		},
		owner: candidate,
	}, nil
}

// GraphCache returns the exact cache installed into every runtime consumer.
func (r *ServingRuntime) GraphCache() *graphcache.GraphCache[string, *pb.Vertex] {
	return r.graph
}

// DurableReceiptWAL reports whether this runtime owns a certified receipt WAL.
// It does not imply that public receipt capability or status is enabled.
func (r *ServingRuntime) DurableReceiptWAL() bool {
	return r != nil && r.receipt != nil
}

// MutationLogStats samples the exact Log installed into both service surfaces.
func (r *ServingRuntime) MutationLogStats() (length, capacity int, evicted uint64) {
	return r.log.Len(), r.log.Cap(), r.log.Evicted()
}

// NewLanternService installs this runtime's exact private state into the
// primary service without enabling any public receipt surface.
func (r *ServingRuntime) NewLanternService(onAppend func()) *LanternService {
	svc := NewLanternService(r.graph)
	svc.origins = r.origins
	svc.runtime = r
	if r.receipt != nil {
		svc.receiptStore = r.receipt.store
	}
	return svc.WithReplication(r.log, r.clock, onAppend)
}

// NewLanternReplicationService installs the same runtime graph, Log, clock,
// origin cut, and private receipt generation into the replication surface.
func (r *ServingRuntime) NewLanternReplicationService(primary *LanternService) (*LanternReplicationService, error) {
	if primary == nil || primary.runtime != r {
		return nil, errors.New("service: replication service requires the primary service from the same runtime")
	}
	svc := NewLanternReplicationService(r.log, r.graph, r.clock)
	svc.runtime = r
	return svc.WithOriginStates(primary), nil
}

// CertifyInstallation verifies that both service surfaces use this runtime's
// exact state instances, then atomically activates the private follower
// coordinator and receipt Snapshot producer for durable mode. It is the narrow
// cross-package barrier used before any network consumer is constructed.
func (r *ServingRuntime) CertifyInstallation(
	primary *LanternService,
	replication *LanternReplicationService,
) error {
	if r == nil || primary == nil || replication == nil {
		return errors.New("service: runtime installation requires both service surfaces")
	}
	if primary.runtime != r || primary.cache != r.graph || primary.log != r.log ||
		primary.clock != r.clock || primary.origins != r.origins {
		return errors.New("service: primary service is not installed from the serving runtime")
	}
	if r.receipt == nil {
		if primary.receiptStore != nil || primary.receiptEdgeDeleteCoordinator != nil {
			return errors.New("service: graph-only runtime installed receipt state")
		}
		if replication.receiptSnapshotRequired || replication.receiptSnapshotSource != nil {
			return errors.New("service: graph-only runtime installed receipt Snapshot state")
		}
	} else if primary.receiptStore != r.receipt.store {
		return errors.New("service: durable runtime receipt Store is not installed")
	}
	if replication.runtime != r || replication.backend != r.graph ||
		replication.log != r.log || replication.clock != r.clock ||
		replication.origins != primary {
		return errors.New("service: replication service is not installed from the serving runtime")
	}
	if r.receipt != nil {
		source, err := NewReceiptWholeStateSource(primary, r.receipt.store)
		if err != nil {
			return fmt.Errorf("service: bind durable receipt follower and Snapshot source: %w", err)
		}
		coordinator := primary.receiptEdgeDeleteCoordinator
		if coordinator == nil || coordinator.service != primary || coordinator.cache != r.graph ||
			coordinator.store != r.receipt.store {
			return errors.New("service: durable receipt follower coordinator is not installed from the serving runtime")
		}
		if replication.receiptSnapshotSource == nil {
			if err := replication.ConfigureReceiptSnapshot(source, r.receipt.policy); err != nil {
				return fmt.Errorf("service: configure durable receipt Snapshot: %w", err)
			}
		} else if !replication.receiptSnapshotRequired ||
			!replication.receiptSnapshotSource.belongsTo(replication) ||
			replication.receiptSnapshotSource.owner != primary ||
			replication.receiptSnapshotSource.store != r.receipt.store ||
			replication.receiptSnapshotPolicy != r.receipt.policy {
			return errors.New("service: durable receipt Snapshot configuration differs from the serving runtime")
		}
		if !replication.receiptSnapshotRequired || replication.receiptSnapshotSource == nil {
			return errors.New("service: durable receipt Snapshot is not installed from the serving runtime")
		}
	}
	return nil
}

// Close releases the runtime owner exactly once. Durable mode closes its Log,
// FileWAL, journals, and lease; graph-only mode closes only its in-memory Log.
func (r *ServingRuntime) Close() error {
	if r == nil {
		return nil
	}
	r.close.Do(func() {
		if r.owner != nil {
			r.closeErr = r.owner.Close()
		}
	})
	return r.closeErr
}
