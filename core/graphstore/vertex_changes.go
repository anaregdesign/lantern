package graphstore

import (
	"bytes"
	"context"
	"errors"

	"github.com/anaregdesign/tarp"
)

// TrackVertexWrites observes writes to the authoritative Vertex row family.
// The callback receives the logical identity, whether the row differs from
// the pinned preimage, and a borrowed encoded value (nil on Delete). It must
// not publish anything, because a caller may repeat the same batch plan.
func (r *Rows[K, V]) TrackVertexWrites(ctx context.Context, preimage *tarp.Snapshot, writer Writer, visit func(K, bool, []byte) error) *VertexWriteTracker[K, V] {
	return &VertexWriteTracker[K, V]{Writer: writer, rows: r, ctx: ctx, preimage: preimage, visit: visit}
}

// DecodeVertexPostimage decodes the value of a present Vertex row observed by
// TrackVertexWrites. Deletion outcomes have no value to decode.
func (r *Rows[K, V]) DecodeVertexPostimage(value []byte) (Vertex[V], error) {
	return r.decodeVertex(value)
}

// VertexWriteTracker forwards writes and records read or decoding errors
// separately from the Writer, which has no error return.
type VertexWriteTracker[K comparable, V any] struct {
	Writer
	rows     *Rows[K, V]
	ctx      context.Context
	preimage *tarp.Snapshot
	visit    func(K, bool, []byte) error
	err      error
}

func (w *VertexWriteTracker[K, V]) Put(key, value []byte) {
	w.observe(key, value)
	w.Writer.Put(key, value)
}

func (w *VertexWriteTracker[K, V]) Delete(key []byte) {
	w.observe(key, nil)
	w.Writer.Delete(key)
}

func (w *VertexWriteTracker[K, V]) observe(key, value []byte) {
	if w.err != nil || len(key) < 10 || key[0] != graphDomain || key[9] != vertexFamily {
		return
	}
	k, err := w.rows.decodeVertexKey(key)
	if err != nil {
		w.err = err
		return
	}
	old, err := w.preimage.GetContext(w.ctx, key)
	var changed bool
	switch {
	case err == nil:
		changed = value == nil || !bytes.Equal(old, value)
	case errors.Is(err, tarp.ErrNotFound):
		changed = value != nil
	default:
		w.err = err
		return
	}
	w.err = w.visit(k, changed, value)
}

func (w *VertexWriteTracker[K, V]) Err() error { return w.err }
