package graphstore

// TrackVertexWrites observes final-operation candidates from the authoritative
// Vertex row family as the existing graph mutation stages its batch. The
// callback borrows each decoded identity and must have no external effects:
// callers may prepare the batch again on the same snapshot.
func (r *Rows[K, V]) TrackVertexWrites(writer Writer, visit func(K, bool)) *VertexWriteTracker[K, V] {
	return &VertexWriteTracker[K, V]{Writer: writer, rows: r, visit: visit}
}

// VertexWriteTracker forwards writes and records key decoding errors separately
// from the Writer, which has no error return.
type VertexWriteTracker[K comparable, V any] struct {
	Writer
	rows  *Rows[K, V]
	visit func(K, bool)
	err   error
}

func (w *VertexWriteTracker[K, V]) Put(key, value []byte) {
	w.observe(key, false)
	w.Writer.Put(key, value)
}

func (w *VertexWriteTracker[K, V]) Delete(key []byte) {
	w.observe(key, true)
	w.Writer.Delete(key)
}

func (w *VertexWriteTracker[K, V]) observe(key []byte, deleted bool) {
	if w.err != nil || len(key) < 10 || key[0] != graphDomain || key[9] != vertexFamily {
		return
	}
	k, err := w.rows.decodeVertexKey(key)
	if err != nil {
		w.err = err
		return
	}
	w.visit(k, deleted)
}

func (w *VertexWriteTracker[K, V]) Err() error { return w.err }
