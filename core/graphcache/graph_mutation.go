package graphcache

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

const (
	GraphMutationMaxItems        = 64
	GraphMutationMaxBucketRows   = 4096
	GraphMutationMaxScratchBytes = 8 << 20
	graphMutationLifetime        = time.Second
	graphMutationLockBudget      = 25 * time.Millisecond
)

var (
	ErrGraphMutationShape  = errors.New("graphcache: invalid mixed graph mutation")
	ErrGraphMutationBudget = errors.New("graphcache: mixed graph mutation budget exhausted")
)

// GraphMutationResult separates the two typed results. Edge indexes are local
// to Edges; the public protocol adds the Vertex item count to those indexes.
type GraphMutationResult[S comparable, T any] struct {
	Vertices VertexPutStageResult[S, T]
	Edges    EdgeAddStageResult[S]
}

// GraphMutationTransaction owns one graph visibility cut. Commit only releases
// locks; Abort restores Edge effects before the union Vertex/dictionary journal.
// The caller must not reenter the cache until this transaction is closed.
type GraphMutationTransaction[S comparable, T any] struct {
	mixed  *stagedGraphMutation[S, T]
	vertex *VertexPutTransaction[S, T]
	edge   *stagedEdgeAdd[S, T]
	edges  []EdgeItem[S]
	stagedMutationLifecycle
}

type stagedGraphMutation[S comparable, T any] struct {
	ctx          context.Context
	deadline     time.Time
	lockedAt     time.Time
	attempts     int
	edges        []EdgeItem[S]
	edgeStage    *stagedEdgeAdd[S, T]
	scratchBytes uint64
}

// BeginGraphMutation stages unconditional Puts followed by explicit-ID Adds.
// Every Add requires live endpoints in the final planned Vertex view, and can
// never create or extend endpoints. Both families must be nonempty.
// ownedPayloadBytes reserves the caller's immutable input, result/envelope and
// encoding scratch in the same fixed budget before graph preparation begins.
func (c *GraphCache[S, T]) BeginGraphMutation(ctx context.Context, vertices []VertexItem[S, T], edges []EdgeItem[S], ts hlc.Timestamp, ownedPayloadBytes uint64) (*GraphMutationTransaction[S, T], error) {
	return c.beginGraphMutation(ctx, vertices, edges, ts, true, ownedPayloadBytes)
}

// BeginReplicatedGraphMutation stages trusted origin effects using receiver
// conflict rules. Empty projections are valid. Origin endpoint admission is not
// repeated, and delayed contributions never revive an endpoint.
// ownedPayloadBytes has the same meaning as on BeginGraphMutation.
func (c *GraphCache[S, T]) BeginReplicatedGraphMutation(ctx context.Context, vertices []VertexItem[S, T], edges []EdgeItem[S], ts hlc.Timestamp, ownedPayloadBytes uint64) (*GraphMutationTransaction[S, T], error) {
	return c.beginGraphMutation(ctx, vertices, edges, ts, false, ownedPayloadBytes)
}

func (c *GraphCache[S, T]) beginGraphMutation(ctx context.Context, vertices []VertexItem[S, T], edges []EdgeItem[S], ts hlc.Timestamp, local bool, ownedPayloadBytes uint64) (*GraphMutationTransaction[S, T], error) {
	started := time.Now()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ownedPayloadBytes > GraphMutationMaxScratchBytes {
		return nil, ErrGraphMutationBudget
	}
	if len(vertices)+len(edges) > GraphMutationMaxItems || (local && (len(vertices) == 0 || len(edges) == 0)) {
		return nil, ErrGraphMutationShape
	}
	seen := make(map[S]bool, len(vertices))
	for _, vertex := range vertices {
		if seen[vertex.Key] {
			return nil, ErrGraphMutationShape
		}
		seen[vertex.Key] = true
	}
	ids := make(map[ContribID]bool, len(edges))
	for _, edge := range edges {
		if edge.ContribID.IsZero() || ids[edge.ContribID] || math.IsNaN(float64(edge.Weight)) || math.IsInf(float64(edge.Weight), 0) {
			return nil, ErrGraphMutationShape
		}
		ids[edge.ContribID] = true
	}
	mixed := &stagedGraphMutation[S, T]{ctx: ctx, deadline: started.Add(graphMutationLifetime), scratchBytes: ownedPayloadBytes}
	if err := mixed.reserveScratch(uint64(len(edges)) * 256); err != nil {
		return nil, err
	}
	mixed.edges = append([]EdgeItem[S](nil), edges...)
	for i := range mixed.edges {
		mixed.edges[i].RequireLiveEndpoints = local
		mixed.edges[i].NoEndpointCreation = true
	}
	vertex, err := c.beginVertexPut(vertices, ts, false, local, mixed)
	if err != nil {
		return nil, err
	}
	return &GraphMutationTransaction[S, T]{vertex: vertex, edge: mixed.edgeStage, edges: mixed.edges, mixed: mixed}, nil
}

func (m *stagedGraphMutation[S, T]) unionKeys(vertices []VertexItem[S, T]) []S {
	keys := make([]S, 0, len(vertices)+2*len(m.edges))
	for _, item := range vertices {
		keys = append(keys, item.Key)
	}
	for _, item := range m.edges {
		keys = append(keys, item.Tail, item.Head)
	}
	return uniqueVertexKeys(keys)
}

func (m *stagedGraphMutation[S, T]) checkPreparation() error {
	m.lockedAt = time.Time{}
	m.attempts++
	// The first acquisition may request initial analysis. Two further
	// acquisitions tolerate replacement/configuration changes, without spinning.
	if m.attempts > 4 {
		return ErrGraphMutationBudget
	}
	return m.checkBudget()
}

func (m *stagedGraphMutation[S, T]) checkBudget() error {
	if err := m.ctx.Err(); err != nil {
		return err
	}
	now := time.Now()
	if now.After(m.deadline) || (!m.lockedAt.IsZero() && now.Sub(m.lockedAt) > graphMutationLockBudget) {
		return ErrGraphMutationBudget
	}
	return nil
}

func (m *stagedGraphMutation[S, T]) prepareLocked(c *GraphCache[S, T], vertices []VertexItem[S, T], applied []int, outcomes []PutOutcome, vertex *stagedVertexMutation[S, T], ts hlc.Timestamp, local bool) error {
	if err := m.checkBudget(); err != nil {
		return err
	}
	if local {
		final := make(map[S]bool, len(applied))
		for _, i := range applied {
			final[vertices[i].Key] = outcomes[i] == PutOutcomeAppliedAndLive
		}
		for _, edge := range m.edges {
			for _, key := range [...]S{edge.Tail, edge.Head} {
				live, planned := final[key]
				if !planned {
					live = c.vertices.HasAt(key, vertex.applicationTime)
				}
				if !live {
					return ErrEdgeEndpointNotLive
				}
			}
		}
	}
	// Count retained rows, including expired ones, before the shared planner
	// clones any bucket. A rejected contribution can also clone a bucket.
	seen := make(map[EdgeKey[S]]bool, len(m.edges))
	rows := 0
	for _, edge := range m.edges {
		key := EdgeKey[S]{Tail: edge.Tail, Head: edge.Head}
		if seen[key] {
			continue
		}
		seen[key] = true
		if bucket := c.edges.bucket(edge.Tail, edge.Head); bucket != nil {
			bucket.mu.Lock()
			rows += len(bucket.values)
			bucket.mu.Unlock()
		}
		if rows > GraphMutationMaxBucketRows {
			return ErrGraphMutationBudget
		}
	}
	// Accepted and rejected contributions can each clone the same bucket;
	// accepted append growth may retain an additional backing array. Include
	// those copies and input rows before entering the shared Edge planner.
	if err := m.reserveScratch(uint64(rows+len(m.edges)) * 512); err != nil {
		return err
	}
	plans, effective, accepted, err := c.planStagedEdgeAddLocked(m.edges, ts, vertex.applicationTime)
	if err != nil {
		return err
	}
	// Edge buckets can allocate dictionary references for untouched endpoints.
	// Capture the full union before either family mutates that shared storage.
	keys := m.unionKeys(vertices)
	vertex.dict.capture(c.dict, keys, len(keys))
	for _, plan := range plans {
		if c.headByTail != nil {
			projected, ok := vertex.projections[plan.key.Head]
			if !ok {
				return ErrGraphMutationShape
			}
			plan.headProjected = projected
		}
	}
	m.edgeStage = &stagedEdgeAdd[S, T]{cache: c, vertex: vertex, plans: plans, effective: effective, accepted: accepted}
	return m.checkBudget()
}

func (tx *GraphMutationTransaction[S, T]) Result() GraphMutationResult[S, T] {
	accepted := make([]IndexedEdgeAdd[S], 0, len(tx.edge.accepted))
	for i, ok := range tx.edge.accepted {
		if ok {
			accepted = append(accepted, IndexedEdgeAdd[S]{Index: i, Item: tx.edges[i]})
		}
	}
	return GraphMutationResult[S, T]{Vertices: tx.vertex.Result(), Edges: EdgeAddStageResult[S]{Effective: append([]float32(nil), tx.edge.effective...), Accepted: accepted}}
}

func (tx *GraphMutationTransaction[S, T]) Commit() {
	if tx == nil || tx.closed {
		panic("graphcache: graph mutation committed after close")
	}
	tx.commit(tx.vertex.Commit)
}

func (tx *GraphMutationTransaction[S, T]) Abort() {
	if tx == nil || tx.closed {
		return
	}
	tx.closed = true
	tx.edge.rollbackEdgesLocked()
	tx.vertex.Abort()
}

// CheckBeforeWAL enforces the mixed deadline immediately before durable publication.
func (tx *GraphMutationTransaction[S, T]) CheckBeforeWAL() error { return tx.mixed.checkBudget() }
