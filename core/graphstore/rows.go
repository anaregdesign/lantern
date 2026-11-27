package graphstore

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

// Rows encodes one graph's physical records. Its methods borrow the caller's
// snapshot or write batch, so graph transitions, receipts and origin progress
// can share one commit. The caller serializes conflicting read-modify-writes
// and discards the entire batch on any preparation error.
type Rows[K comparable, V any] struct {
	keys   tarp.Codec[K]
	values tarp.Codec[V]
}

// NewRows binds deterministic, reversible codecs. The key codec supplies the
// projection whose byte order and raw byte prefixes scans use. StringCodec
// is the identity projection for string keys. Value codecs must preserve all
// distinct present values, including nil when supported by the application.
func NewRows[K comparable, V any](keys tarp.Codec[K], values tarp.Codec[V]) (*Rows[K, V], error) {
	if nilCodec(keys) || nilCodec(values) {
		return nil, fmt.Errorf("%w: nil graph codec", ErrInvalidRow)
	}
	return &Rows[K, V]{keys, values}, nil
}

// ValueMatches compares an application value to its exact stored representation.
func (r *Rows[K, V]) ValueMatches(value V, stored []byte) (bool, error) {
	encoded, err := r.values.Encode(value)
	return err == nil && bytes.Equal(encoded, stored), err
}

func nilCodec(c any) bool {
	if c == nil {
		return true
	}
	v := reflect.ValueOf(c)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

type Vertex[V any] struct {
	Value      V
	Expiration time.Time
	HLC        hlc.Timestamp
}

type EdgeKey[K comparable] struct{ Tail, Head K }

// Edge holds a Put base and its surviving LWW floor. LastOrdinal is the
// high-water mark for distinct zero-ID local Adds, independent of TTL.
type Edge struct {
	HLC         hlc.Timestamp
	LastOrdinal uint64
	Base        *Source
}

type Source struct {
	Weight           float32
	Expiration       time.Time
	HLC              hlc.Timestamp
	DerivedAggregate bool
}

// SourceID addresses one Add. Exactly one of ContribID and Ordinal is nonzero.
// The base Put is stored separately on the Edge.
type SourceID struct {
	ContribID [24]byte
	Ordinal   uint64
}

type FloorKey[K comparable] struct {
	Kind            FloorKind
	Key, Tail, Head K
	ContribID       [24]byte
}

type Floor struct {
	HLC        hlc.Timestamp
	Expiration time.Time
}

type VertexEntry[K comparable, V any] struct {
	Key    K
	Vertex Vertex[V]
}
type EdgeEntry[K comparable] struct {
	Key  EdgeKey[K]
	Edge Edge
}
type SourceEntry struct {
	ID     SourceID
	Source Source
}
type FloorEntry[K comparable] struct {
	Key   FloorKey[K]
	Floor Floor
}

func readRow[T any](snap *tarp.Snapshot, key []byte, decode func([]byte) (T, error)) (T, bool, error) {
	return readChargedRow(snap, key, decode, nil)
}

func readChargedRow[T any](snap *tarp.Snapshot, key []byte, decode func([]byte) (T, error), charge func(int) error) (T, bool, error) {
	var zero T
	value, err := snap.Get(key)
	absent := errors.Is(err, tarp.ErrNotFound)
	if err != nil && !absent {
		return zero, false, err
	}
	if charge != nil {
		if chargeErr := charge(len(key) + len(value)); chargeErr != nil {
			return zero, false, chargeErr
		}
	}
	if absent {
		return zero, false, nil
	}
	result, err := decode(value)
	if err != nil {
		return zero, false, err
	}
	return result, true, nil
}

func (r *Rows[K, V]) GetVertex(snap *tarp.Snapshot, gen uint64, k K) (Vertex[V], bool, error) {
	key, err := r.vertexKey(gen, vertexFamily, k)
	if err != nil {
		return Vertex[V]{}, false, err
	}
	return readRow(snap, key, r.decodeVertex)
}

func (r *Rows[K, V]) PutVertex(b Writer, gen uint64, k K, v Vertex[V]) error {
	key, err := r.vertexKey(gen, vertexFamily, k)
	if err != nil {
		return err
	}
	data, err := r.encodeVertex(v)
	if err != nil {
		return err
	}
	// The small projection shares the entire commit (including generation
	// switches), and can answer TTL without ever loading a Vertex payload.
	projection, err := r.vertexKey(gen, vertexMetadataFamily, k)
	if err != nil {
		return err
	}
	b.Put(key, data)
	b.Put(projection, data[:metadataSize])
	return nil
}

func (r *Rows[K, V]) DeleteVertex(b Writer, gen uint64, k K) error {
	key, err := r.vertexKey(gen, vertexFamily, k)
	if err != nil {
		return err
	}
	projection, err := r.vertexKey(gen, vertexMetadataFamily, k)
	if err != nil {
		return err
	}
	b.Delete(key)
	b.Delete(projection)
	return nil
}

func (r *Rows[K, V]) GetEdge(snap *tarp.Snapshot, gen uint64, k EdgeKey[K]) (Edge, bool, error) {
	key, err := r.edgeKey(gen, edgeFamily, k)
	if err != nil {
		return Edge{}, false, err
	}
	return readRow(snap, key, decodeEdge)
}

func (r *Rows[K, V]) PutEdge(b Writer, gen uint64, k EdgeKey[K], e Edge) error {
	key, err := r.edgeKey(gen, edgeFamily, k)
	if err != nil {
		return err
	}
	b.Put(key, encodeEdge(e))
	return nil
}

// DeleteEdge stages deletion of the header. Call DeleteSources when removing
// the bucket's Adds; resets can independently preserve newer contributions.
func (r *Rows[K, V]) DeleteEdge(b Writer, gen uint64, k EdgeKey[K]) error {
	key, err := r.edgeKey(gen, edgeFamily, k)
	if err != nil {
		return err
	}
	b.Delete(key)
	return nil
}

func decodeAdd(data []byte) (Source, error) {
	s, err := decodeSource(data)
	if err == nil && s.DerivedAggregate {
		return Source{}, fmt.Errorf("%w: aggregate provenance on Add", ErrInvalidRow)
	}
	return s, err
}

func (r *Rows[K, V]) GetSource(snap *tarp.Snapshot, gen uint64, k EdgeKey[K], id SourceID) (Source, bool, error) {
	key, err := r.sourceKey(gen, sourceFamily, k, id)
	if err != nil {
		return Source{}, false, err
	}
	return readRow(snap, key, decodeAdd)
}

func (r *Rows[K, V]) PutSource(b Writer, gen uint64, k EdgeKey[K], id SourceID, s Source) error {
	if s.DerivedAggregate {
		return fmt.Errorf("%w: aggregate provenance on Add", ErrInvalidRow)
	}
	key, err := r.sourceKey(gen, sourceFamily, k, id)
	if err != nil {
		return err
	}
	b.Put(key, appendSource(nil, s))
	return nil
}

func (r *Rows[K, V]) DeleteSource(b Writer, gen uint64, k EdgeKey[K], id SourceID) error {
	key, err := r.sourceKey(gen, sourceFamily, k, id)
	if err != nil {
		return err
	}
	b.Delete(key)
	return nil
}
