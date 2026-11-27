package graphstore

import (
	"context"
	"fmt"
	"iter"
	"math"

	"github.com/anaregdesign/tarp"
)

// EnsureTopologyStats certifies the active generation before serving. This
// derived build does not advance the application revision.
func (db *DB) EnsureTopologyStats(ctx context.Context) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	snap, err := db.topologyBuildSnapshot(ctx)
	if err != nil {
		return err
	}
	defer snap.Close()
	return buildTopologyStats(ctx, snap, db.head.active, db.apply)
}

// BuildTopologyStats certifies a hidden generation after all of its Edge
// headers and incoming keys have been staged. The caller owns that graph
// image and must finish this build before publishing or changing its Edges.
func (g *Generation) BuildTopologyStats(ctx context.Context) error {
	db := g.db
	db.mu.Lock()
	if db.staging != g {
		db.mu.Unlock()
		return ErrGenerationClosed
	}
	snap, err := db.topologyBuildSnapshot(ctx)
	db.mu.Unlock()
	if err != nil {
		return err
	}
	defer snap.Close()
	apply := func(batch *tarp.WriteBatch) error {
		db.mu.Lock()
		defer db.mu.Unlock()
		if db.staging != g {
			return ErrGenerationClosed
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return db.apply(batch)
	}
	return buildTopologyStats(ctx, snap, g.id, apply)
}

// Caller holds db.mu.
func (db *DB) topologyBuildSnapshot(ctx context.Context) (*tarp.Snapshot, error) {
	if err := db.available(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return db.raw.Snapshot()
}

func buildTopologyStats(ctx context.Context, snap *tarp.Snapshot, gen uint64, apply func(*tarp.WriteBatch) error) error {
	if _, ready, err := readTopologyStats(snap, gen); err != nil || ready {
		return err
	}
	incoming, err := hasIncomingEdgeIndex(snap, gen)
	if err != nil {
		return err
	}
	if !incoming {
		return fmt.Errorf("%w: topology build requires the incoming Edge projection", ErrInvalidRow)
	}
	totals, err := familyPrefix(gen, topologyFamily)
	if err != nil {
		return err
	}
	builder := topologyBuildBatch{apply: apply, chunk: generationWriter{id: gen}}
	// An aborted startup can leave working rows. Remove them before rebuilding
	// so changes made by startup expiry cannot leave a stale endpoint count.
	lo, hi := tarp.PrefixRange(totals)
	for key, err := range snap.KeysRange(ctx, lo, hi) {
		if err != nil {
			return err
		}
		if err := builder.stage(ctx, key, nil, true); err != nil {
			return err
		}
	}
	stats, err := buildTopologyDegrees(ctx, snap, gen, func(key []byte, degree TopologyDegree) error {
		return builder.stage(ctx, key, encodeTopologyCounts(degree.In, degree.Out), false)
	})
	if err != nil {
		return err
	}
	if err := builder.stage(ctx, totals, encodeTopologyCounts(stats.NonemptyTails, stats.Edges), false); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return builder.flush()
}

type topologyBuildBatch struct {
	apply func(*tarp.WriteBatch) error
	chunk generationWriter
}

func (b *topologyBuildBatch) stage(ctx context.Context, key, value []byte, delete bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cost, err := rowSize(b.chunk.id, key, value)
	if err != nil {
		return err
	}
	if b.chunk.batch.Len() >= 512 || (b.chunk.batch.Len() != 0 && b.chunk.bytes+cost > 1<<20) {
		if err := b.flush(); err != nil {
			return err
		}
	}
	if delete {
		b.chunk.Delete(key)
	} else {
		b.chunk.Put(key, value)
	}
	return b.chunk.err
}

func (b *topologyBuildBatch) flush() error {
	if err := b.apply(&b.chunk.batch); err != nil {
		return err
	}
	b.chunk.batch.Reset()
	b.chunk.bytes = 0
	return nil
}

type topologyCount struct {
	identity string
	count    uint64
}

// groupedTopology scans one ordered Edge family. The first encoded identity
// is the tail in the primary family and the head in the incoming family.
func groupedTopology(ctx context.Context, snap *tarp.Snapshot, gen uint64, family byte) iter.Seq2[topologyCount, error] {
	return func(yield func(topologyCount, error) bool) {
		prefix, err := familyPrefix(gen, family)
		if err != nil {
			yield(topologyCount{}, err)
			return
		}
		bounds := prefixBounds(prefix)
		if family == incomingEdgeFamily {
			bounds = bounds.after(prefix, false)
		}
		var current topologyCount
		for key, err := range snap.KeysRange(ctx, bounds.lower, bounds.upper) {
			if err != nil {
				yield(topologyCount{}, err)
				return
			}
			tuple, err := edgeTupleCodec.Decode(key)
			if err != nil {
				yield(topologyCount{}, fmt.Errorf("%w: decode topology Edge: %w", ErrInvalidRow, err))
				return
			}
			if current.count != 0 && current.identity != tuple.Tail {
				if !yield(current, nil) {
					return
				}
				current.count = 0
			}
			current.identity = tuple.Tail
			if err := changeTopologyCount(&current.count, true); err != nil {
				yield(topologyCount{}, err)
				return
			}
		}
		if current.count != 0 {
			yield(current, nil)
		}
	}
}

func buildTopologyDegrees(ctx context.Context, snap *tarp.Snapshot, gen uint64, stage func([]byte, TopologyDegree) error) (TopologyStats, error) {
	var stats TopologyStats
	var incomingEdges uint64
	nextIn, stopIn := iter.Pull2(groupedTopology(ctx, snap, gen, incomingEdgeFamily))
	defer stopIn()
	nextOut, stopOut := iter.Pull2(groupedTopology(ctx, snap, gen, edgeFamily))
	defer stopOut()
	in, inErr, hasIn := nextIn()
	out, outErr, hasOut := nextOut()
	for hasIn || hasOut {
		if inErr != nil {
			return TopologyStats{}, inErr
		}
		if outErr != nil {
			return TopologyStats{}, outErr
		}
		identity := out.identity
		if !hasOut || hasIn && in.identity < identity {
			identity = in.identity
		}
		var degree TopologyDegree
		if hasIn && in.identity == identity {
			degree.In = in.count
			in, inErr, hasIn = nextIn()
		}
		if hasOut && out.identity == identity {
			degree.Out = out.count
			out, outErr, hasOut = nextOut()
		}
		if math.MaxUint64-incomingEdges < degree.In || math.MaxUint64-stats.Edges < degree.Out {
			return TopologyStats{}, fmt.Errorf("%w: topology total overflow", ErrInvalidRow)
		}
		incomingEdges += degree.In
		stats.Edges += degree.Out
		if degree.Out != 0 {
			// Each nonempty tail contributes at least one Edge.
			stats.NonemptyTails++
		}
		key, err := topologyKey(gen, identity)
		if err != nil {
			return TopologyStats{}, err
		}
		if err := stage(key, degree); err != nil {
			return TopologyStats{}, err
		}
	}
	if incomingEdges != stats.Edges {
		return TopologyStats{}, fmt.Errorf("%w: incoming and primary Edge counts differ", ErrInvalidRow)
	}
	return stats, ctx.Err()
}
