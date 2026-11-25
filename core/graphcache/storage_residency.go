package graphcache

import (
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/tarp"
)

type storageResidency[S comparable, T any] struct {
	rows        *graphstore.Rows[S, T]
	db          *graphstore.DB
	fault       atomic.Pointer[error]
	pending     storageSweep[S]
	collecting  bool
	maintenance func(func() error) error
}

// StorageError reports a required row that could not be hydrated. Recovery
// starts a fresh serving runtime. A failed read must never publish a partial
// mutation or a response built from incomplete state.
func (c *GraphCache[S, T]) StorageError() error {
	if c.storage != nil {
		if fault := c.storage.fault.Load(); fault != nil {
			return *fault
		}
	}
	return nil
}

func (s *storageResidency[S, T]) fence(err error) error {
	s.fault.CompareAndSwap(nil, &err)
	return err
}

func (s *storageResidency[S, T]) vertex(key S, expiry time.Time) (T, error) {
	var value T
	err := s.db.WithCommittedSnapshot(func(g uint64, snap *tarp.Snapshot) error {
		v, found, err := s.rows.GetVertex(snap, g, key)
		if err != nil {
			return err
		}
		if !found || !v.Expiration.Equal(expiry) {
			return fmt.Errorf("%w: cold vertex does not match the committed row", graphstore.ErrInvalidRow)
		}
		value = v.Value
		return nil
	})
	if err != nil {
		return value, s.fence(err)
	}
	return value, nil
}

// StoragePublished drops the payloads in a freshly committed publication
// without entering the aggregate graph lock, which a staged writer can own.
func (c *GraphCache[S, T]) StoragePublished(keys StorageKeys[S]) {
	if c.storage == nil {
		return
	}
	for _, key := range keys.Vertices {
		c.vertices.ReleaseValue(key)
	}
	releaseEdge := func(tail, head S) {
		if w := c.edges.bucket(tail, head); w != nil {
			c.storage.bindWeight(graphstore.EdgeKey[S]{Tail: tail, Head: head}, w)
		}
	}
	for _, e := range keys.Edges {
		releaseEdge(e.Tail, e.Head)
		c.vertices.ReleaseValue(e.Tail)
		c.vertices.ReleaseValue(e.Head)
	}
	for _, e := range keys.Contributions {
		releaseEdge(e.Tail, e.Head)
		c.vertices.ReleaseValue(e.Tail)
		c.vertices.ReleaseValue(e.Head)
	}
}

// StorageGenerationPublished releases the payloads of a newly installed
// complete image. The publisher owns the exclusive serving publication cut.
func (c *GraphCache[S, T]) StorageGenerationPublished() {
	if c.storage == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.vertices.SetValueLoader(c.storage.vertex)
	c.vertices.ReleaseValues()
	c.storage.pending = storageSweep[S]{}
	c.bindStorageGCHooks()
	c.edges.rangeBuckets(func(tail, head S, w *weight) bool {
		c.storage.bindWeight(graphstore.EdgeKey[S]{Tail: tail, Head: head}, w)
		return true
	})
}

// ReadVertex preserves the distinction between a missing vertex and an
// unreadable payload for clients of a disk-backed GraphCache.
func (c *GraphCache[S, T]) ReadVertex(key S) (T, bool, error) {
	v, ok := c.GetVertex(key)
	return v, ok, c.StorageError()
}

func (c *GraphCache[S, T]) bindStorage(rows *graphstore.Rows[S, T], view *graphstore.ReadView) error {
	if rows == nil || view == nil || view.Database() == nil {
		return errors.New("graphcache: storage requires bound rows and read view")
	}
	c.storage = &storageResidency[S, T]{rows: rows, db: view.Database()}
	c.vertices.SetValueLoader(c.storage.vertex)
	c.bindStorageGCHooks()
	return nil
}
