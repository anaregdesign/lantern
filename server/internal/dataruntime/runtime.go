package dataruntime

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
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
	tarp                      *tarpRelay
	graph                     *graphcache.GraphCache[string, *pb.Vertex]
	log                       *mutationlog.Log
	clock                     *hlc.Clock
	origins                   *originStateTracker
	receipt                   *receiptServingRuntime
	owner                     io.Closer
	namespaceFormat           string
	replicationSendMaxBytes   int
	replicationFrameCertified bool
	closed                    atomic.Bool
	close                     sync.Once
	closeErr                  error
	graphPolicy               atomic.Pointer[GraphPolicyOwner]
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
	startupRestore          *receiptStartupRestore
	recaptureOnRestore      bool
	sidecarFault            func(receiptBaselineSidecarFaultPoint) error
	installFault            func(receiptBaselineInstallFaultPoint) error
	backupCertified         bool
	publicEnabled           atomic.Bool
	noLongerProvableLookups atomic.Uint64
}

// ReceiptDatabaseConfig is the validated input for a fresh or
// same-epoch restart. The caller supplies the production graph policy before
// replay; all durable ownership remains inside the returned ServingRuntime.
type ReceiptDatabaseConfig struct {
	Path            string
	NamespaceFormat string
	Receipt         mutationreceipt.Config
	Log             mutationlog.Options
	DefaultTTL      time.Duration
	ConfigureGraph  func(*graphcache.GraphCache[string, *pb.Vertex]) error
	NodeID          hlc.NodeID
	Now             time.Time
	BaselineCodec   ReceiptBaselineArchiveCodec
	startupRestore  *receiptStartupRestore
}

// receiptStartupRestore is immutable backup-set evidence prepared for one
// durable startup. Fresh mode installs Capture directly after creating a new
// epoch. Restart fallback uses Capture as the proven WAL boundary, replays the
// current suffix, then captures that complete recovered runtime before
// publishing a new canonical baseline.
type receiptStartupRestore struct {
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
	namespaceFormat ...string,
) (*ServingRuntime, error) {
	if graph == nil || log == nil || clock == nil {
		return nil, errors.New("service: graph-only runtime requires graph, log, and clock")
	}
	format := ""
	if len(namespaceFormat) > 1 {
		return nil, errors.New("service: multiple namespace formats")
	}
	if len(namespaceFormat) == 1 {
		format = namespaceFormat[0]
	}
	if err := validateDataFormat(format); err != nil {
		return nil, err
	}
	if err := ensureRuntimeEdgeHistory(graph, format); err != nil {
		return nil, err
	}
	return &ServingRuntime{
		graph: graph, log: log, clock: clock, origins: newOriginStateTracker(), owner: log,
		namespaceFormat: format,
	}, nil
}

func validateReceiptDatabaseConfig(config ReceiptDatabaseConfig) error {
	if err := validateDataFormat(config.NamespaceFormat); err != nil {
		return err
	}
	if config.Path == "" || !filepath.IsAbs(config.Path) ||
		config.ConfigureGraph == nil || config.Log.WAL != nil {
		return errors.New("service: receipt database requires an absolute path, graph policy, and no preconfigured journal")
	}

	if config.NodeID == (hlc.NodeID{}) {
		return errors.New("service: receipt database requires a nonzero node ID")
	}
	if _, err := mutationreceipt.New(config.Receipt); err != nil {
		return fmt.Errorf("service: receipt database policy: %w", err)
	}
	return nil
}

func ensureRuntimeEdgeHistory(graph *graphcache.GraphCache[string, *pb.Vertex], namespace string) error {
	if namespace == "" || graph.RetainsDanglingEdgeHistory() {
		return nil
	}
	if graph.VertexCount() != 0 || graph.EdgeCount() != 0 {
		return errors.New("service: namespaced graph must select retained Edge history before writes")
	}
	graph.RetainDanglingEdgeHistory()
	return nil
}

func durableRuntimeGraphPolicy(config ReceiptDatabaseConfig) func(*graphcache.GraphCache[string, *pb.Vertex]) error {
	return func(graph *graphcache.GraphCache[string, *pb.Vertex]) error {
		if err := ensureRuntimeEdgeHistory(graph, config.NamespaceFormat); err != nil {
			return err
		}
		return config.ConfigureGraph(graph)
	}
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

// GraphCache returns the exact cache installed into every runtime consumer.
// NodeID is the origin used by both local mutation stamps and replication.
func (r *ServingRuntime) NodeID() hlc.NodeID { return r.clock.NodeID() }

func (r *ServingRuntime) GraphCache() *graphcache.GraphCache[string, *pb.Vertex] {
	return r.graph
}

func (r *ServingRuntime) DataNamespaceFormat() string {
	if r == nil {
		return ""
	}
	return r.namespaceFormat
}

// SharesServingRuntime is a trusted composition check, never a public grant.
func (s *Executor) SharesServingRuntime(runtime *ServingRuntime) bool {
	graph, ok := s.cache.(*graphcache.GraphCache[string, *pb.Vertex])
	return runtime != nil && s.runtime == runtime && ok && graph == runtime.graph
}

// ReceiptsEnabled reports whether this runtime owns the durable receipt ledger.
// Serving public receipt requests also requires the native activation barrier.
func (r *ServingRuntime) ReceiptsEnabled() bool {
	return r != nil && r.receipt != nil
}

// MutationLogStats samples the exact Log installed into both execution surfaces.
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

// NewExecutor installs this runtime's exact private state into the
// primary executor without enabling any public receipt surface.
func (r *ServingRuntime) NewExecutor(onAppend func()) *Executor {
	svc := NewExecutor(r.graph)
	svc.origins = r.origins
	svc.runtime = r
	if r.GraphStore() != nil {
		r.graph.SetStorageMaintenance(func(tick func() error) error {
			gate := svc.lockPublicationGate("gc", false)
			defer gate.Unlock()
			if svc.publicGraphFaultLocked() {
				return publicationGapError()
			}
			return tick()
		})
	}
	if r.namespaceFormat != "" {
		svc.WithDataNamespace()
	}
	if r.receipt != nil {
		svc.receiptStore = r.receipt.store
		svc.receiptRetiredCatalog = r.receipt.retired
	}
	return svc.WithReplication(r.log, r.clock, onAppend)
}

// NewReplication installs the same runtime graph, Log, clock,
// origin cut, and private receipt generation into the replication surface.
func (r *ServingRuntime) NewReplication(primary *Executor) (*Replication, error) {
	if primary == nil || primary.runtime != r {
		return nil, errors.New("service: replication service requires the primary service from the same runtime")
	}
	svc := NewReplication(r.log, r.graph, r.clock)
	svc.runtime = r
	return svc.WithOriginStates(primary), nil
}

// CertifyInstallation verifies that both execution surfaces use this runtime's
// exact state instances, then atomically activates the private follower
// coordinator and receipt Snapshot producer for durable mode. It is the narrow
// cross-package barrier used before any network consumer is constructed.
func (r *ServingRuntime) CertifyInstallation(
	primary *Executor,
	replication *Replication,
) error {
	return r.CertifyInstallationWithReplicationSendLimit(primary, replication, 0)
}

// CertifyInstallationWithReplicationSendLimit additionally binds mutation
// admission and the replication handler to one immutable protobuf message
// limit. A zero limit is explicitly unlimited.
func (r *ServingRuntime) CertifyInstallationWithReplicationSendLimit(
	primary *Executor,
	replication *Replication,
	maxSendBytes int,
) error {
	if r == nil || primary == nil || replication == nil {
		return errors.New("service: runtime installation requires both service surfaces")
	}
	if maxSendBytes < 0 {
		return errors.New("service: replication send limit must be zero (unlimited) or positive")
	}
	if primary.runtime != r || primary.cache != r.graph || primary.log != r.log ||
		primary.clock != r.clock || primary.origins != r.origins ||
		primary.namespaceFormat != r.namespaceFormat || replication.namespaceFormat != r.namespaceFormat {
		return errors.New("service: primary service is not installed from the serving runtime")
	}
	if r.namespaceFormat != "" && !r.graph.RetainsDanglingEdgeHistory() {
		return errors.New("service: private runtime lacks retained Edge history policy")
	}
	if r.receipt == nil {
		if primary.receiptStore != nil || primary.receiptRetiredCatalog != nil ||
			primary.receiptEdgeAddCoordinator != nil ||
			primary.receiptEdgeDeleteCoordinator != nil ||
			primary.receiptEdgeContributionDeleteCoordinator != nil ||
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
	if err := r.log.VisitRetained(func(entry mutationlog.Entry) error {
		if _, err := validateReplicationFrameSize(entry.Op, maxSendBytes); err != nil {
			return fmt.Errorf(
				"service: retained replication frame at local log seq %d is not streamable: %w",
				entry.Seq,
				err,
			)
		}
		if _, err := validateReplicationRelayFrameSize(entry.Op, maxSendBytes); err != nil {
			return fmt.Errorf(
				"service: retained replication frame at local log seq %d has an unstreamable receiver-local relay: %w",
				entry.Seq,
				err,
			)
		}
		return nil
	}); err != nil {
		return err
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
		contributionDelete := primary.receiptEdgeContributionDeleteCoordinator
		if contributionDelete == nil || contributionDelete.service != primary ||
			contributionDelete.cache != r.graph || contributionDelete.store != r.receipt.store {
			return errors.New("service: durable receipt contribution Delete coordinator is not installed from the serving runtime")
		}
		edgeAdd := primary.receiptEdgeAddCoordinator
		if edgeAdd == nil || edgeAdd.service != primary || edgeAdd.cache != r.graph ||
			edgeAdd.store != r.receipt.store {
			return errors.New("service: durable receipt Edge Add coordinator is not installed from the serving runtime")
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
	primary *Executor,
	replication *Replication,
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

// CertifyReceiptBackup binds the exact runtime/executor/replication cut to the
// production whole-state backup source. Public receipts remain disabled until
// this proof and bearer-auth activation both complete.
func (r *ServingRuntime) CertifyReceiptBackup(
	primary *Executor,
	replication *Replication,
) error {
	if _, _, err := r.ReceiptWholeStateBackupSource(primary, replication); err != nil {
		return err
	}
	r.receipt.backupCertified = true
	return nil
}

// ActivatePublicReceipts enables capability, status, and receipt-bearing Vertex
// Put, exact Vertex Delete, exact Edge Delete, and contribution-keyed Edge Add
// only for the durable cut certified for recovery, replication, and backup.
func (r *ServingRuntime) ActivatePublicReceipts(
	primary *Executor,
	replication *Replication,
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

// Close drains the relay and releases the backing store exactly once.
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
