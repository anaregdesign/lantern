package graphstore

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"

	"github.com/anaregdesign/tarp"
)

// TopologyDegree counts structurally retained Edge buckets. Source weights,
// endpoint visibility and expiration at read time do not change the count.
type TopologyDegree struct {
	In, Out uint64
}

// TopologyStats is the retained graph corpus used by Edge weighting.
type TopologyStats struct {
	NonemptyTails, Edges uint64
}

func (s TopologyStats) valid() bool {
	return s.NonemptyTails <= s.Edges && (s.NonemptyTails != 0 || s.Edges == 0)
}

// GetTopologyStats returns the corpus totals on one pinned cut. An absent
// totals row means this generation has no certified topology projection.
func (r *Rows[K, V]) GetTopologyStats(snap *tarp.Snapshot, gen uint64) (TopologyStats, bool, error) {
	return readTopologyStats(snap, gen)
}

func readTopologyStats(snap *tarp.Snapshot, gen uint64) (TopologyStats, bool, error) {
	key, err := familyPrefix(gen, topologyFamily)
	if err != nil {
		return TopologyStats{}, false, err
	}
	return readRow(snap, key, func(value []byte) (TopologyStats, error) {
		if len(value) != 16 {
			return TopologyStats{}, fmt.Errorf("%w: invalid topology totals size", ErrInvalidRow)
		}
		stats := TopologyStats{binary.BigEndian.Uint64(value), binary.BigEndian.Uint64(value[8:])}
		if !stats.valid() {
			return TopologyStats{}, fmt.Errorf("%w: inconsistent topology totals", ErrInvalidRow)
		}
		return stats, nil
	})
}

// HasTopologyStats checks the completion record on the same pinned cut.
func (r *Rows[K, V]) HasTopologyStats(snap *tarp.Snapshot, gen uint64) (bool, error) {
	_, found, err := readTopologyStats(snap, gen)
	return found, err
}

// GetTopologyDegree returns the structurally retained counts for key. A
// missing row represents zero counts on a cut with certified topology stats.
func (r *Rows[K, V]) GetTopologyDegree(snap *tarp.Snapshot, gen uint64, key K) (TopologyDegree, error) {
	physical, err := r.vertexKey(gen, topologyFamily, key)
	if err != nil {
		return TopologyDegree{}, err
	}
	return readTopologyDegree(snap, physical)
}

func readTopologyDegree(snap *tarp.Snapshot, physical []byte) (TopologyDegree, error) {
	degree, _, err := readRow(snap, physical, func(value []byte) (TopologyDegree, error) {
		if len(value) != 16 {
			return TopologyDegree{}, fmt.Errorf("%w: invalid topology degree size", ErrInvalidRow)
		}
		degree := TopologyDegree{binary.BigEndian.Uint64(value), binary.BigEndian.Uint64(value[8:])}
		if degree == (TopologyDegree{}) {
			return TopologyDegree{}, fmt.Errorf("%w: empty topology degree row", ErrInvalidRow)
		}
		return degree, nil
	})
	return degree, err
}

func topologyKey(gen uint64, encoded string) ([]byte, error) {
	return vertexTupleCodec.Encode(vertexTuple{rowHeader{graphDomain, gen, topologyFamily}, encoded})
}

func encodeTopologyCounts(first, second uint64) []byte {
	var value [16]byte
	binary.BigEndian.PutUint64(value[:8], first)
	binary.BigEndian.PutUint64(value[8:], second)
	return value[:]
}

// topologyChanges observes the incoming keys co-written with Edge headers.
// Header-only updates leave those keys untouched. The final state per pair
// handles repeated writes without counting intermediate operations.
type topologyChanges struct {
	keys map[string]bool
}

func (t *topologyChanges) note(key []byte, present bool) {
	if len(key) <= 10 || key[0] != graphDomain || key[9] != incomingEdgeFamily {
		return
	}
	if t.keys == nil {
		t.keys = make(map[string]bool)
	}
	t.keys[string(key)] = present
}

type topologyDegreeChange struct {
	key []byte
	TopologyDegree
}

func (t *topologyChanges) finish(ctx context.Context, snap *tarp.Snapshot, gen uint64, w Writer) error {
	if len(t.keys) == 0 {
		return nil
	}
	stats, ready, err := readTopologyStats(snap, gen)
	if err != nil || !ready {
		return err
	}
	initial := stats
	degrees := make(map[string]topologyDegreeChange)
	load := func(encoded string) (topologyDegreeChange, error) {
		if change, ok := degrees[encoded]; ok {
			return change, nil
		}
		key, err := topologyKey(gen, encoded)
		if err != nil {
			return topologyDegreeChange{}, err
		}
		degree, err := readTopologyDegree(snap, key)
		if err == nil && (degree.In > initial.Edges || degree.Out > initial.Edges) {
			err = fmt.Errorf("%w: topology degree exceeds the retained Edge count", ErrInvalidRow)
		}
		return topologyDegreeChange{key, degree}, err
	}
	for key, present := range t.keys {
		physical := []byte(key)
		existed := false
		for _, err := range snap.KeysRange(ctx, physical, append(bytes.Clone(physical), 0)) {
			if err != nil {
				return err
			}
			existed = true
			break
		}
		if existed == present {
			continue
		}
		reverse, err := edgeTupleCodec.Decode(physical)
		if err != nil {
			return fmt.Errorf("%w: decode topology transition: %w", ErrInvalidRow, err)
		}
		tail, err := load(reverse.Head)
		if err != nil {
			return err
		}
		wasNonempty := tail.Out != 0
		if err := changeTopologyCount(&tail.Out, present); err != nil {
			return err
		}
		degrees[reverse.Head] = tail
		if nowNonempty := tail.Out != 0; nowNonempty != wasNonempty {
			if err := changeTopologyCount(&stats.NonemptyTails, nowNonempty); err != nil {
				return err
			}
		}
		// Load after recording the out-degree so self-loops update both
		// counters of the same Vertex row.
		head, err := load(reverse.Tail)
		if err != nil {
			return err
		}
		if err := changeTopologyCount(&head.In, present); err != nil {
			return err
		}
		degrees[reverse.Tail] = head
		if err := changeTopologyCount(&stats.Edges, present); err != nil {
			return err
		}
	}
	if !stats.valid() {
		return fmt.Errorf("%w: topology transitions produce inconsistent totals", ErrInvalidRow)
	}
	for _, change := range degrees {
		if change.TopologyDegree == (TopologyDegree{}) {
			w.Delete(change.key)
		} else {
			w.Put(change.key, encodeTopologyCounts(change.In, change.Out))
		}
	}
	if stats != initial {
		key, err := familyPrefix(gen, topologyFamily)
		if err != nil {
			return err
		}
		w.Put(key, encodeTopologyCounts(stats.NonemptyTails, stats.Edges))
	}
	return ctx.Err()
}

func changeTopologyCount(count *uint64, increase bool) error {
	if increase {
		if *count == math.MaxUint64 {
			return fmt.Errorf("%w: topology counter overflow", ErrInvalidRow)
		}
		*count++
	} else {
		if *count == 0 {
			return fmt.Errorf("%w: topology counter underflow", ErrInvalidRow)
		}
		*count--
	}
	return nil
}
