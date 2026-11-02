package dataruntime

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// GraphPolicyConfig contains only operational admission and GC policy. Zero
// admission limits mean unlimited; a zero edge budget means a full GC sweep.
// Lowering a cap never evicts data, causal identities or retained floors.
type GraphPolicyConfig struct {
	MaxVertices            int
	MaxEdges               int
	MaxVertexCausalEntries int
	MaxEdgeCausalEntries   int
	GCEdgeBudget           int
	GCInterval             time.Duration
}

func (c GraphPolicyConfig) Validate() error {
	if c.MaxVertices < 0 || c.MaxEdges < 0 || c.MaxVertexCausalEntries < 0 || c.MaxEdgeCausalEntries < 0 || c.GCEdgeBudget < 0 {
		return errors.New("graph policy: capacity limits and GC edge budget must be nonnegative")
	}
	if c.GCInterval <= 0 {
		return errors.New("graph policy: GC interval must be positive")
	}
	return nil
}

func (c GraphPolicyConfig) configureCore(graph *graphcache.GraphCache[string, *pb.Vertex]) {
	graph.SetCausalMetadataLimits(graphcache.CausalMetadataLimits{
		MaxVertexEntries: c.MaxVertexCausalEntries,
		MaxEdgeEntries:   c.MaxEdgeCausalEntries,
	})
	graph.SetGCEdgeBudget(c.GCEdgeBudget)
}

// GraphPolicySnapshot is one immutable effective generation. Generation zero
// is externally configured bootstrap policy; committed managed policy is >0.
type GraphPolicySnapshot struct {
	Generation uint64
	Config     GraphPolicyConfig
}

var (
	ErrGraphPolicyUnavailable = errors.New("graph policy: owner is unavailable")
	ErrGraphPolicyConflict    = errors.New("graph policy: generation conflicts")
	ErrGraphPolicyStopped     = errors.New("graph policy: GC owner has stopped")
	ErrGraphPolicyRunning     = errors.New("graph policy: GC owner is already running")
)

// AvailableGraphPolicy checks one real constructed resource without changing
// policy or starting another watcher.
func (o *GraphPolicyOwner) AvailableGraphPolicy() error {
	if o == nil || o.current.Load() == nil || o.runtime == nil || o.primary == nil || !o.primary.SharesServingRuntime(o.runtime) || o.watch == nil {
		return ErrGraphPolicyUnavailable
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.stopped || o.runtime.closed.Load() {
		return ErrGraphPolicyStopped
	}
	return nil
}

// GraphPolicyOwner serializes policy transitions and owns exactly one GC
// watcher. The transition mutex is never acquired by graph readers or hooks.
// Cancellation is joined before acquiring the publication cut, so hooks may
// safely finish a committed-view read while a policy change waits for them.
type GraphPolicyOwner struct {
	mu      sync.Mutex
	current atomic.Pointer[GraphPolicySnapshot]
	runtime *ServingRuntime
	primary *Executor
	watch   func(context.Context, time.Duration)
	parent  context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	running bool
	stopped bool
}

// NewGraphPolicyOwner binds one owner to the exact restored serving runtime.
// It does not start GC. Composition must pass this owner as the listener's
// Watcher instead of starting an additional GraphCache.Watch goroutine.
func NewGraphPolicyOwner(runtime *ServingRuntime, primary *Executor, bootstrap GraphPolicyConfig) (*GraphPolicyOwner, error) {
	if runtime == nil || primary == nil || !primary.SharesServingRuntime(runtime) {
		return nil, errors.New("graph policy: requires the exact serving runtime and primary executor")
	}
	if err := bootstrap.Validate(); err != nil {
		return nil, err
	}
	owner := &GraphPolicyOwner{runtime: runtime, primary: primary, watch: runtime.graph.Watch}
	owner.current.Store(&GraphPolicySnapshot{Config: bootstrap})
	if err := primary.withExclusiveCommittedView(func() error {
		if runtime.closed.Load() || runtime.graphPolicy.Load() != nil {
			return errors.New("graph policy: runtime is closed or already has an owner")
		}
		bootstrap.configureCore(runtime.graph)
		runtime.graphPolicy.Store(owner)
		return nil
	}); err != nil {
		return nil, err
	}
	return owner, nil
}

func (o *GraphPolicyOwner) Snapshot() GraphPolicySnapshot { return *o.current.Load() }

// Apply installs verified committed policy after external authority/CAS. Core
// setters retain existing state. Admission, future candidates and observers see
// the new snapshot only after all selected Core settings have been installed.
func (o *GraphPolicyOwner) Apply(next GraphPolicySnapshot) error {
	if err := next.Config.Validate(); err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.stopped || o.runtime.closed.Load() {
		return ErrGraphPolicyStopped
	}
	current := o.Snapshot()
	if next.Generation == 0 || next.Generation < current.Generation ||
		(next.Generation == current.Generation && next != current) {
		return ErrGraphPolicyConflict
	}
	if next == current {
		return nil
	}
	restart := o.running && (next.Config.GCInterval != current.Config.GCInterval || next.Config.GCEdgeBudget != current.Config.GCEdgeBudget)
	if restart {
		o.joinWorkerLocked()
	}
	err := o.primary.withExclusiveCommittedView(func() error {
		if o.runtime.closed.Load() {
			return ErrGraphPolicyStopped
		}
		next.Config.configureCore(o.runtime.graph)
		o.current.Store(&next)
		return nil
	})
	if restart && !o.runtime.closed.Load() {
		// On publication refusal the old effective snapshot is still current;
		// restart exactly that policy rather than leaving GC silently stopped.
		o.startWorkerLocked()
	}
	return err
}

// Watch implements the existing listener lifecycle seam. The legacy interval
// argument is intentionally superseded by the owner's effective snapshot.
func (o *GraphPolicyOwner) Watch(ctx context.Context, _ time.Duration) {
	_ = o.RunGC(ctx)
}

// RunGC owns one watcher until ctx cancellation and joins its final hooks.
func (o *GraphPolicyOwner) RunGC(ctx context.Context) error {
	o.mu.Lock()
	if o.stopped || o.runtime.closed.Load() {
		o.mu.Unlock()
		return ErrGraphPolicyStopped
	}
	if o.running {
		o.mu.Unlock()
		return ErrGraphPolicyRunning
	}
	o.parent = ctx
	o.running = true
	o.startWorkerLocked()
	o.mu.Unlock()
	<-ctx.Done()
	o.mu.Lock()
	defer o.mu.Unlock()
	o.joinWorkerLocked()
	o.running = false
	o.stopped = true
	return nil
}

func (o *GraphPolicyOwner) startWorkerLocked() {
	ctx, cancel := context.WithCancel(o.parent)
	o.cancel = cancel
	o.done = make(chan struct{})
	done := o.done
	interval := o.Snapshot().Config.GCInterval
	go func() {
		defer close(done)
		o.watch(ctx, interval)
	}()
}

func (o *GraphPolicyOwner) joinWorkerLocked() {
	if o.cancel != nil {
		o.cancel()
		<-o.done
		o.cancel = nil
		o.done = nil
	}
}

// GraphPolicyPreview reports O(1) connected-node counters, not an exact
// per-request admission prediction. GC may progress between the separately
// sampled Core counters. Aggregate caps remain conservative soft caps;
// replication and restoration may exceed every local admission limit.
type GraphPolicyPreview struct {
	Current               GraphPolicySnapshot
	VertexFootprint       int
	EdgeFootprint         int
	VertexCausalEntries   int
	EdgeCausalEntries     int
	VerticesOverLimit     bool
	EdgesOverLimit        bool
	VertexCausalOverLimit bool
	EdgeCausalOverLimit   bool
}

func (o *GraphPolicyOwner) Preview(next GraphPolicyConfig) (GraphPolicyPreview, error) {
	if err := next.Validate(); err != nil {
		return GraphPolicyPreview{}, err
	}
	o.primary.replicationCutMu.RLock()
	defer o.primary.replicationCutMu.RUnlock()
	if o.primary.publicGraphFaultLocked() {
		return GraphPolicyPreview{}, publicationGapError()
	}
	vertices, edges := o.runtime.graph.CapacityFootprint()
	causal := o.runtime.graph.CausalMetadataStats()
	return GraphPolicyPreview{
		Current: o.Snapshot(), VertexFootprint: vertices, EdgeFootprint: edges,
		VertexCausalEntries: causal.VertexEntries, EdgeCausalEntries: causal.EdgeEntries,
		VerticesOverLimit:     next.MaxVertices > 0 && vertices > next.MaxVertices,
		EdgesOverLimit:        next.MaxEdges > 0 && edges > next.MaxEdges,
		VertexCausalOverLimit: next.MaxVertexCausalEntries > 0 && causal.VertexEntries > next.MaxVertexCausalEntries,
		EdgeCausalOverLimit:   next.MaxEdgeCausalEntries > 0 && causal.EdgeEntries > next.MaxEdgeCausalEntries,
	}, nil
}

// configureGraphCandidate preserves the immutable external/index constructor,
// then overlays current operational admission policy on the detached graph.
func (r *ServingRuntime) configureGraphCandidate(graph *graphcache.GraphCache[string, *pb.Vertex]) error {
	if err := r.receipt.configureGraph(graph); err != nil {
		return err
	}
	r.configureCandidateOperationalPolicy(graph)
	return nil
}

// The final baseline caller holds the publication cut; its snapshot cannot
// race Apply. Updating limits changes no graph/receipt/auth data or indexes.
func (r *ServingRuntime) configureCandidateOperationalPolicy(graph *graphcache.GraphCache[string, *pb.Vertex]) {
	if owner := r.graphPolicy.Load(); owner != nil {
		owner.Snapshot().Config.configureCore(graph)
	}
}

func (s *Executor) capacityLimits() CapacityLimits {
	if s.runtime != nil {
		if owner := s.runtime.graphPolicy.Load(); owner != nil {
			config := owner.Snapshot().Config
			return CapacityLimits{MaxVertices: config.MaxVertices, MaxEdges: config.MaxEdges}
		}
	}
	if configured := s.capacity.Load(); configured != nil {
		return *configured
	}
	return CapacityLimits{}
}
