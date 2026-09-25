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
	"sync/atomic"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// ServingRuntime is the single process-owned state cut installed before any
// listener or replication worker is constructed. Constructing a durable
// runtime alone does not enable receipt capability, status, or receipt-bearing
// mutation RPCs; production activation requires the final certified barrier.
type ServingRuntime struct {
	graph                     *graphcache.GraphCache[string, *pb.Vertex]
	log                       *mutationlog.Log
	clock                     *hlc.Clock
	origins                   *originStateTracker
	receipt                   *receiptServingRuntime
	owner                     io.Closer
	replicationSendMaxBytes   int
	replicationFrameCertified bool
	closed                    atomic.Bool
	close                     sync.Once
	closeErr                  error
}

type receiptServingRuntime struct {
	store                   *mutationreceipt.Store
	retired                 *retiredReceiptCatalogSlot
	policy                  mutationreceipt.Config
	epoch                   mutationreceipt.Epoch
	generation              [16]byte
	committedBaseline       receiptBaselineReference
	operationAdmission      *receiptOperationAdmission
	baselineCodec           ReceiptBaselineArchiveCodec
	defaultTTL              time.Duration
	configureGraph          func(*graphcache.GraphCache[string, *pb.Vertex]) error
	owner                   *receiptWALOwnedCandidate
	startupRestore          *ReceiptStartupRestore
	recaptureOnRestore      bool
	sidecarFault            func(receiptBaselineSidecarFaultPoint) error
	installFault            func(receiptBaselineInstallFaultPoint) error
	backupCertified         bool
	publicEnabled           atomic.Bool
	noLongerProvableLookups atomic.Uint64
}

const (
	receiptRuntimeGenerationMagic = "LNRGEN1\n"
	receiptRuntimeGenerationSize  = len(receiptRuntimeGenerationMagic) + sha256.Size + 16 + 1 + sha256.Size
)

type receiptRuntimeGenerationRecord struct {
	generation       [16]byte
	requiresBaseline bool
}

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
	BaselineCodec  ReceiptBaselineArchiveCodec
	StartupRestore *ReceiptStartupRestore
}

// ReceiptStartupRestore is immutable backup-set evidence prepared for one
// durable startup. Fresh mode installs Capture directly after creating a new
// epoch. Restart fallback uses Capture as the proven WAL boundary, replays the
// current suffix, then captures that complete recovered runtime before
// publishing a new canonical baseline.
type ReceiptStartupRestore struct {
	Capture        ReceiptWholeStateCapture
	WALCut         mutationlog.FileWALTipWitness
	NodeID         hlc.NodeID
	Generation     [16]byte
	ArchivedEpoch  mutationreceipt.Epoch
	ArchivedPolicy [sha256.Size]byte
	BackupSetID    uint64
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
	if config.StartupRestore != nil {
		if err := validateFreshReceiptStartupRestore(config, *config.StartupRestore); err != nil {
			return nil, err
		}
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
		config.StartupRestore != nil,
	)
	if err != nil {
		return nil, errors.Join(err, candidate.Close())
	}
	runtime, err := certifyReceiptWALServingRuntime(candidate, config, generation, receiptBaselineReference{})
	if err != nil {
		return nil, err
	}
	if config.StartupRestore != nil {
		restore := cloneReceiptStartupRestore(*config.StartupRestore)
		runtime.receipt.startupRestore = &restore
	}
	return runtime, nil
}

// OpenDurableReceiptWALServingRuntime resumes one complete genesis WAL under
// its lease. Missing, corrupt, mismatched, truncated, or unreplayable state is
// rejected without rotating or reinitializing any durable bytes.
func OpenDurableReceiptWALServingRuntime(config DurableReceiptWALRuntimeConfig) (*ServingRuntime, error) {
	if err := validateDurableReceiptWALRuntimeConfig(config); err != nil {
		return nil, err
	}
	if config.StartupRestore != nil {
		return nil, errors.New("service: normal durable receipt WAL restart cannot carry backup restore evidence")
	}
	now := config.Now
	if now.IsZero() {
		now = time.Now()
	}
	var generation [16]byte
	var requiresBaseline bool
	preflight := func(canonicalPath string, policy [sha256.Size]byte) error {
		var readErr error
		var record receiptRuntimeGenerationRecord
		record, readErr = readReceiptRuntimeGenerationRecord(
			canonicalPath,
			config.Receipt.Epoch,
			policy,
			config.NodeID,
		)
		generation = record.generation
		requiresBaseline = record.requiresBaseline
		if readErr == nil && requiresBaseline && config.BaselineCodec == nil {
			return errors.New(
				"service: durable receipt WAL startup restore requires the combined baseline codec",
			)
		}
		return readErr
	}
	var candidate *receiptWALOwnedCandidate
	var err error
	if config.BaselineCodec == nil {
		candidate, err = openLeasedReceiptWALCandidate(
			config.Path, config.Receipt, now, config.Log, config.DefaultTTL,
			config.ConfigureGraph, preflight,
		)
	} else {
		validateBaseline := func(scan receiptBaselineWALScan) error {
			if requiresBaseline && !scan.hasMarker {
				return errors.New("service: durable receipt WAL requires its committed startup restore baseline")
			}
			if scan.hasMarker && scan.firstGeneration != generation {
				return errors.New("service: receipt baseline generation does not descend from genesis")
			}
			return nil
		}
		candidate, err = openLeasedReceiptWALCandidateWithBaseline(
			config.Path, config.Receipt, now, config.Log, config.DefaultTTL,
			config.ConfigureGraph, config.NodeID, config.BaselineCodec, validateBaseline, preflight,
		)
	}
	if err != nil {
		return nil, err
	}
	activeGeneration := generation
	var committedBaseline receiptBaselineReference
	if candidate.baseline.hasMarker {
		if candidate.baseline.firstGeneration != generation {
			return nil, errors.Join(
				errors.New("service: receipt baseline generation does not descend from genesis"),
				candidate.Close(),
			)
		}
		activeGeneration = candidate.baseline.activeGeneration
		committedBaseline = candidate.baseline.marker.reference()
	}
	return certifyReceiptWALServingRuntime(candidate, config, activeGeneration, committedBaseline)
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

func clearReceiptClockHighWater(config mutationreceipt.Config) mutationreceipt.Config {
	config.ClockHighWater = time.Time{}
	return config
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
	requiresBaseline bool,
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
	record := encodeReceiptRuntimeGeneration(
		walPath,
		epoch,
		policy,
		nodeID,
		generation,
		requiresBaseline,
	)
	if _, err := file.Write(record); err != nil {
		return generation, fmt.Errorf("service: write receipt runtime generation: %w", err)
	}
	if err := file.Sync(); err != nil {
		return generation, fmt.Errorf("service: sync receipt runtime generation: %w", err)
	}
	if err := syncReceiptDirectory(filepath.Dir(path)); err != nil {
		return generation, fmt.Errorf("service: sync receipt runtime generation directory: %w", err)
	}
	return generation, nil
}

func readReceiptRuntimeGeneration(
	walPath string,
	epoch mutationreceipt.Epoch,
	policy [sha256.Size]byte,
	nodeID hlc.NodeID,
) ([16]byte, error) {
	record, err := readReceiptRuntimeGenerationRecord(walPath, epoch, policy, nodeID)
	return record.generation, err
}

func readReceiptRuntimeGenerationRecord(
	walPath string,
	epoch mutationreceipt.Epoch,
	policy [sha256.Size]byte,
	nodeID hlc.NodeID,
) (receiptRuntimeGenerationRecord, error) {
	var recordValue receiptRuntimeGenerationRecord
	path := walPath + ".generation"
	info, err := os.Lstat(path)
	if err != nil {
		return recordValue, fmt.Errorf("service: stat receipt runtime generation: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() != int64(receiptRuntimeGenerationSize) {
		return recordValue, errors.New("service: receipt runtime generation is corrupt")
	}
	file, err := os.Open(path)
	if err != nil {
		return recordValue, fmt.Errorf("service: open receipt runtime generation: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return recordValue, fmt.Errorf("service: stat receipt runtime generation: %w", err)
	}
	if !os.SameFile(info, opened) {
		return recordValue, errors.New("service: receipt runtime generation changed during open")
	}
	record := make([]byte, receiptRuntimeGenerationSize)
	if _, err := io.ReadFull(file, record); err != nil {
		return recordValue, fmt.Errorf("service: read receipt runtime generation: %w", err)
	}
	final, err := os.Lstat(path)
	if err != nil {
		return recordValue, fmt.Errorf("service: restat receipt runtime generation: %w", err)
	}
	if !os.SameFile(info, final) {
		return recordValue, errors.New("service: receipt runtime generation changed during read")
	}
	wantBinding := receiptRuntimeGenerationBinding(walPath, epoch, policy, nodeID)
	if !bytes.Equal(record[:len(receiptRuntimeGenerationMagic)], []byte(receiptRuntimeGenerationMagic)) ||
		!bytes.Equal(record[len(receiptRuntimeGenerationMagic):len(receiptRuntimeGenerationMagic)+sha256.Size], wantBinding[:]) {
		return recordValue, errors.New("service: receipt runtime generation binding mismatch")
	}
	checksumStart := receiptRuntimeGenerationSize - sha256.Size
	checksum := sha256.Sum256(record[:checksumStart])
	if !bytes.Equal(record[checksumStart:], checksum[:]) {
		return recordValue, errors.New("service: receipt runtime generation checksum mismatch")
	}
	generationStart := len(receiptRuntimeGenerationMagic) + sha256.Size
	copy(recordValue.generation[:], record[generationStart:generationStart+16])
	if recordValue.generation == ([16]byte{}) {
		return receiptRuntimeGenerationRecord{}, errors.New("service: receipt runtime generation is zero")
	}
	switch record[generationStart+16] {
	case 0:
	case 1:
		recordValue.requiresBaseline = true
	default:
		return receiptRuntimeGenerationRecord{}, errors.New(
			"service: receipt runtime generation baseline requirement is invalid",
		)
	}
	return recordValue, nil
}

func encodeReceiptRuntimeGeneration(
	walPath string,
	epoch mutationreceipt.Epoch,
	policy [sha256.Size]byte,
	nodeID hlc.NodeID,
	generation [16]byte,
	requiresBaseline bool,
) []byte {
	record := make([]byte, 0, receiptRuntimeGenerationSize)
	record = append(record, receiptRuntimeGenerationMagic...)
	binding := receiptRuntimeGenerationBinding(walPath, epoch, policy, nodeID)
	record = append(record, binding[:]...)
	record = append(record, generation[:]...)
	if requiresBaseline {
		record = append(record, 1)
	} else {
		record = append(record, 0)
	}
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
	config DurableReceiptWALRuntimeConfig,
	generation [16]byte,
	committedBaseline receiptBaselineReference,
) (_ *ServingRuntime, err error) {
	if candidate == nil || candidate.state == nil || candidate.state.graph == nil ||
		candidate.state.receipts == nil || candidate.state.origins == nil ||
		candidate.state.log == nil || candidate.walProvenance == nil ||
		candidate.lease == nil || generation == ([16]byte{}) {
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
	policy := clearReceiptClockHighWater(config.Receipt)
	policyStore, err := mutationreceipt.New(policy)
	if err != nil {
		return nil, fmt.Errorf("service: certify receipt Snapshot policy: %w", err)
	}
	if candidate.state.receipts.Epoch() != policy.Epoch ||
		candidate.state.receipts.PolicyFingerprint() != policyStore.PolicyFingerprint() {
		return nil, errors.New("service: durable receipt WAL policy does not match recovered Store")
	}
	receiptState, err := candidate.state.receipts.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("service: certify recovered receipt Store: %w", err)
	}
	retired, err := newRetiredReceiptCatalogSlot(
		policy,
		receiptState.ClockHighWaterMillis,
		candidate.state.retired,
	)
	if err != nil {
		return nil, fmt.Errorf("service: certify recovered retired receipt catalog: %w", err)
	}

	clock := hlc.New(config.NodeID, hlc.Options{})
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
			store:              candidate.state.receipts,
			retired:            retired,
			policy:             policy,
			epoch:              candidate.state.receipts.Epoch(),
			generation:         generation,
			committedBaseline:  committedBaseline,
			operationAdmission: newReceiptOperationAdmission(),
			baselineCodec:      config.BaselineCodec,
			defaultTTL:         config.DefaultTTL,
			configureGraph:     config.ConfigureGraph,
			owner:              candidate,
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

func (r *ServingRuntime) receiptWALTipWitness(
	primary *LanternService,
	expectedSeq uint64,
) (mutationlog.FileWALTipWitness, error) {
	if r == nil || r.receipt == nil || r.receipt.owner == nil {
		return mutationlog.FileWALTipWitness{}, errors.New("service: durable receipt WAL witness is unavailable")
	}
	if primary == nil || primary.runtime != r || primary.cache != r.graph ||
		primary.log != r.log || primary.clock != r.clock ||
		primary.origins != r.origins || primary.receiptStore != r.receipt.store ||
		primary.receiptRetiredCatalog != r.receipt.retired {
		return mutationlog.FileWALTipWitness{}, errors.New("service: durable receipt WAL witness service differs from runtime")
	}
	owner, ok := r.owner.(*receiptWALOwnedCandidate)
	if !ok || owner != r.receipt.owner || owner.state == nil ||
		owner.state.graph != r.graph || owner.state.log != r.log ||
		owner.state.origins != r.origins || owner.state.receipts != r.receipt.store ||
		owner.walProvenance == nil || owner.lease == nil {
		return mutationlog.FileWALTipWitness{}, errors.New("service: durable receipt WAL witness provenance differs from runtime")
	}
	var witness mutationlog.FileWALTipWitness
	err := owner.lease.WithPath(func(canonicalPath string) error {
		var witnessErr error
		witness, witnessErr = owner.walProvenance.TipWitness(canonicalPath)
		return witnessErr
	})
	if err != nil {
		return mutationlog.FileWALTipWitness{}, fmt.Errorf("service: durable receipt WAL witness: %w", err)
	}
	if witness.Seq != expectedSeq {
		return mutationlog.FileWALTipWitness{}, fmt.Errorf(
			"service: durable receipt WAL witness: %w: captured seq %d differs from WAL seq %d",
			mutationlog.ErrFileWALSequence,
			expectedSeq,
			witness.Seq,
		)
	}
	return witness, nil
}

// MutationLogStats samples the exact Log installed into both service surfaces.
func (r *ServingRuntime) MutationLogStats() (length, capacity int, evicted uint64) {
	return r.log.Len(), r.log.Cap(), r.log.Evicted()
}

// ReceiptStats samples the active bounded Store. A graph-only runtime returns
// the zero value and never exposes receipt metrics as capability.
func (r *ServingRuntime) ReceiptStats() mutationreceipt.Stats {
	if r == nil || r.receipt == nil || r.receipt.store == nil {
		return mutationreceipt.Stats{}
	}
	stats := r.receipt.store.Stats()
	stats.NoLongerProvableLookups = r.receipt.noLongerProvableLookups.Load()
	return stats
}

// NewLanternService installs this runtime's exact private state into the
// primary service without enabling any public receipt surface.
func (r *ServingRuntime) NewLanternService(onAppend func()) *LanternService {
	svc := NewLanternService(r.graph)
	svc.origins = r.origins
	svc.runtime = r
	if r.receipt != nil {
		svc.receiptStore = r.receipt.store
		svc.receiptRetiredCatalog = r.receipt.retired
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
	return r.CertifyInstallationWithReplicationSendLimit(primary, replication, 0)
}

// CertifyInstallationWithReplicationSendLimit additionally binds mutation
// admission and the replication handler to one immutable protobuf message
// limit. A zero limit is explicitly unlimited.
func (r *ServingRuntime) CertifyInstallationWithReplicationSendLimit(
	primary *LanternService,
	replication *LanternReplicationService,
	maxSendBytes int,
) error {
	if r == nil || primary == nil || replication == nil {
		return errors.New("service: runtime installation requires both service surfaces")
	}
	if maxSendBytes < 0 {
		return errors.New("service: replication send limit must be zero (unlimited) or positive")
	}
	if primary.runtime != r || primary.cache != r.graph || primary.log != r.log ||
		primary.clock != r.clock || primary.origins != r.origins {
		return errors.New("service: primary service is not installed from the serving runtime")
	}
	if r.receipt == nil {
		if primary.receiptStore != nil || primary.receiptRetiredCatalog != nil ||
			primary.receiptEdgeDeleteCoordinator != nil ||
			primary.receiptVertexPutCoordinator != nil ||
			primary.receiptVertexDeleteCoordinator != nil {
			return errors.New("service: graph-only runtime installed receipt state")
		}
		if replication.receiptSnapshotRequired || replication.receiptSnapshotSource != nil {
			return errors.New("service: graph-only runtime installed receipt Snapshot state")
		}
	} else if primary.receiptStore != r.receipt.store ||
		primary.receiptRetiredCatalog != r.receipt.retired {
		return errors.New("service: durable runtime receipt state is not installed")
	}
	if replication.runtime != r || replication.backend != r.graph ||
		replication.log != r.log || replication.clock != r.clock ||
		replication.origins != primary {
		return errors.New("service: replication service is not installed from the serving runtime")
	}
	if r.replicationFrameCertified &&
		(r.replicationSendMaxBytes != maxSendBytes ||
			!primary.replicationFrameCertified ||
			primary.replicationSendMaxBytes != maxSendBytes ||
			!replication.replicationFrameCertified ||
			replication.replicationSendMaxBytes != maxSendBytes) {
		return errors.New("service: replication send limit differs from the certified serving runtime")
	}
	if maxSendBytes > 0 {
		for _, entry := range r.log.RetainedEntries() {
			if _, err := validateReplicationFrameSize(entry.Op, maxSendBytes); err != nil {
				return fmt.Errorf(
					"service: retained replication frame at local log seq %d is not streamable: %w",
					entry.Seq,
					err,
				)
			}
		}
	}
	if r.receipt != nil {
		source, err := NewReceiptWholeStateSource(primary, r.receipt.store)
		if err != nil {
			return fmt.Errorf("service: bind durable receipt follower and Snapshot source: %w", err)
		}
		coordinator := primary.receiptEdgeDeleteCoordinator
		if coordinator == nil || coordinator.service != primary || coordinator.cache != r.graph ||
			coordinator.store != r.receipt.store || coordinator.retired != r.receipt.retired {
			return errors.New("service: durable receipt follower coordinator is not installed from the serving runtime")
		}
		vertexPut := primary.receiptVertexPutCoordinator
		if vertexPut == nil || vertexPut.service != primary || vertexPut.cache != r.graph ||
			vertexPut.store != r.receipt.store {
			return errors.New("service: durable receipt Vertex Put coordinator is not installed from the serving runtime")
		}
		vertexDelete := primary.receiptVertexDeleteCoordinator
		if vertexDelete == nil || vertexDelete.service != primary || vertexDelete.cache != r.graph ||
			vertexDelete.store != r.receipt.store {
			return errors.New("service: durable receipt Vertex Delete coordinator is not installed from the serving runtime")
		}
		if replication.receiptSnapshotSource == nil {
			if err := replication.ConfigureReceiptSnapshot(source, r.receipt.policy); err != nil {
				return fmt.Errorf("service: configure durable receipt Snapshot: %w", err)
			}
		} else if !replication.receiptSnapshotRequired ||
			!replication.receiptSnapshotSource.belongsTo(replication) ||
			replication.receiptSnapshotSource.owner != primary ||
			replication.receiptSnapshotSource.store != r.receipt.store ||
			replication.receiptSnapshotSource.retired != r.receipt.retired ||
			replication.receiptSnapshotPolicy != r.receipt.policy {
			return errors.New("service: durable receipt Snapshot configuration differs from the serving runtime")
		}
		if !replication.receiptSnapshotRequired || replication.receiptSnapshotSource == nil {
			return errors.New("service: durable receipt Snapshot is not installed from the serving runtime")
		}
	}
	r.replicationSendMaxBytes = maxSendBytes
	r.replicationFrameCertified = true
	primary.replicationSendMaxBytes = maxSendBytes
	primary.replicationFrameCertified = true
	replication.replicationSendMaxBytes = maxSendBytes
	replication.replicationFrameCertified = true
	return nil
}

// ReceiptWholeStateBackupSource returns the exact source installed during
// runtime certification. It exposes no receipt mutation or status surface.
func (r *ServingRuntime) ReceiptWholeStateBackupSource(
	primary *LanternService,
	replication *LanternReplicationService,
) (*ReceiptWholeStateSource, mutationreceipt.Config, error) {
	if r == nil || r.receipt == nil || primary == nil || replication == nil ||
		primary.runtime != r || replication.runtime != r ||
		primary.cache != r.graph || primary.log != r.log ||
		primary.clock != r.clock || primary.origins != r.origins ||
		primary.receiptStore != r.receipt.store ||
		primary.receiptRetiredCatalog != r.receipt.retired ||
		replication.backend != r.graph || replication.log != r.log ||
		replication.clock != r.clock || replication.origins != primary ||
		!replication.receiptSnapshotRequired ||
		replication.receiptSnapshotSource == nil ||
		!replication.receiptSnapshotSource.belongsTo(replication) ||
		replication.receiptSnapshotSource.owner != primary ||
		replication.receiptSnapshotSource.store != r.receipt.store ||
		replication.receiptSnapshotSource.retired != r.receipt.retired ||
		replication.receiptSnapshotPolicy != r.receipt.policy ||
		!r.replicationFrameCertified ||
		!primary.replicationFrameCertified ||
		!replication.replicationFrameCertified ||
		primary.replicationSendMaxBytes != r.replicationSendMaxBytes ||
		replication.replicationSendMaxBytes != r.replicationSendMaxBytes {
		return nil, mutationreceipt.Config{}, errors.New(
			"service: receipt backup source requires the exact certified serving runtime",
		)
	}
	if r.clock.NodeID() == (hlc.NodeID{}) || r.receipt.generation == ([16]byte{}) {
		return nil, mutationreceipt.Config{}, errors.New("service: certified receipt backup identity is invalid")
	}
	return replication.receiptSnapshotSource, r.receipt.policy, nil
}

// CertifyReceiptBackup binds the exact runtime/service/replication cut to the
// production whole-state backup source. Public receipts remain disabled until
// this proof and bearer-auth activation both complete.
func (r *ServingRuntime) CertifyReceiptBackup(
	primary *LanternService,
	replication *LanternReplicationService,
) error {
	if _, _, err := r.ReceiptWholeStateBackupSource(primary, replication); err != nil {
		return err
	}
	r.receipt.backupCertified = true
	return nil
}

// ActivatePublicReceipts enables capability, status, and receipt-bearing Edge
// Delete only for the exact durable cut already certified for recovery,
// replication, and backup.
func (r *ServingRuntime) ActivatePublicReceipts(
	primary *LanternService,
	replication *LanternReplicationService,
) error {
	if r == nil || r.closed.Load() || r.receipt == nil ||
		r.receipt.startupRestore != nil || !r.receipt.backupCertified {
		return errors.New("service: public receipts require a live recovery- and backup-certified durable runtime")
	}
	if !r.replicationFrameCertified ||
		!primary.replicationFrameCertified ||
		!replication.replicationFrameCertified ||
		primary.replicationSendMaxBytes != r.replicationSendMaxBytes ||
		replication.replicationSendMaxBytes != r.replicationSendMaxBytes {
		return errors.New("service: public receipts require certified replication frame admission")
	}
	if _, _, err := r.ReceiptWholeStateBackupSource(primary, replication); err != nil {
		return err
	}
	r.receipt.publicEnabled.Store(true)
	return nil
}

// Close releases the runtime owner exactly once. Durable mode closes its Log,
// FileWAL, journals, and lease; graph-only mode closes only its in-memory Log.
func (r *ServingRuntime) Close() error {
	if r == nil {
		return nil
	}
	r.close.Do(func() {
		r.closed.Store(true)
		if r.owner != nil {
			r.closeErr = r.owner.Close()
		}
	})
	return r.closeErr
}
