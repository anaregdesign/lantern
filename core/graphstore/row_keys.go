package graphstore

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/anaregdesign/tarp"
)

// The domain, uint64 generation, family byte, and ordered field encodings
// form the on-disk graph schema. Independent record families can use different
// tuple shapes in the same Tarp DB.
const graphDomain byte = 'G'

const (
	vertexFamily         byte = 1
	edgeFamily           byte = 2
	sourceFamily         byte = 3
	vertexMetadataFamily byte = 9
	incomingEdgeFamily   byte = 10
	topologyFamily       byte = 11
	sourceOrderFamily    byte = 12
)

type FloorKind byte

const (
	VertexBarrier         FloorKind = 4
	EdgeBarrier           FloorKind = 5
	VertexTombstone       FloorKind = 6
	EdgeTombstone         FloorKind = 7
	ContributionTombstone FloorKind = 8
)

type rowHeader struct {
	Domain     byte
	Generation uint64
	Family     byte
}

type vertexTuple struct {
	Header rowHeader
	Key    string
}

type edgeTuple struct {
	Header     rowHeader
	Tail, Head string
}

type sourceTuple struct {
	Edge     edgeTuple
	Kind     byte
	Identity [24]byte
}

const localSource byte = 1
const explicitSource byte = 2

var (
	vertexTupleCodec = orderedTuple[vertexTuple]()
	edgeTupleCodec   = orderedTuple[edgeTuple]()
	sourceTupleCodec = orderedTuple[sourceTuple]()
	ErrInvalidRow    = errors.New("graphstore: invalid graph row")
)

func orderedTuple[K any]() tarp.Codec[K] {
	codec, err := tarp.NewOrderedCodec[K]()
	if err != nil {
		panic(err) // All callers construct fixed package-owned schemas.
	}
	return codec
}

func header(generation uint64, family byte) (rowHeader, error) {
	if generation == 0 {
		return rowHeader{}, fmt.Errorf("%w: generation zero", ErrInvalidRow)
	}
	return rowHeader{graphDomain, generation, family}, nil
}

func familyPrefix(generation uint64, family byte) ([]byte, error) {
	h, err := header(generation, family)
	if err != nil {
		return nil, err
	}
	return tarp.EncodeOrderedTuplePrefix(h)
}

func (r *Rows[K, V]) vertexKey(generation uint64, family byte, key K) ([]byte, error) {
	h, err := header(generation, family)
	if err != nil {
		return nil, err
	}
	p, err := r.keys.Encode(key)
	if err != nil {
		return nil, err
	}
	return vertexTupleCodec.Encode(vertexTuple{h, string(p)})
}

func (r *Rows[K, V]) edgeTuple(generation uint64, family byte, k EdgeKey[K]) (edgeTuple, error) {
	h, err := header(generation, family)
	if err != nil {
		return edgeTuple{}, err
	}
	tail, err := r.keys.Encode(k.Tail)
	if err != nil {
		return edgeTuple{}, err
	}
	head, err := r.keys.Encode(k.Head)
	if err != nil {
		return edgeTuple{}, err
	}
	return edgeTuple{h, string(tail), string(head)}, nil
}

func (r *Rows[K, V]) edgeKey(generation uint64, family byte, key EdgeKey[K]) ([]byte, error) {
	tuple, err := r.edgeTuple(generation, family, key)
	if err != nil {
		return nil, err
	}
	return edgeTupleCodec.Encode(tuple)
}

func (id SourceID) diskIdentity() (byte, [24]byte, error) {
	var local [24]byte
	if id.ContribID != local && id.Ordinal == 0 {
		return explicitSource, id.ContribID, nil
	}
	if id.ContribID == local && id.Ordinal != 0 {
		binary.BigEndian.PutUint64(local[16:], id.Ordinal)
		return localSource, local, nil
	}
	return 0, local, fmt.Errorf("%w: source requires exactly one explicit ID or positive ordinal", ErrInvalidRow)
}

func (r *Rows[K, V]) sourceKey(generation uint64, family byte, key EdgeKey[K], id SourceID) ([]byte, error) {
	edge, err := r.edgeTuple(generation, family, key)
	if err != nil {
		return nil, err
	}
	kind, identity, err := id.diskIdentity()
	if err != nil {
		return nil, err
	}
	return sourceTupleCodec.Encode(sourceTuple{edge, kind, identity})
}

func sourceIdentity(k sourceTuple) (SourceID, error) {
	id := SourceID{ContribID: k.Identity}
	switch k.Kind {
	case explicitSource:
		if id.ContribID != ([24]byte{}) {
			return id, nil
		}
	case localSource:
		ordinal := binary.BigEndian.Uint64(k.Identity[16:])
		if ([16]byte)(k.Identity[:16]) == ([16]byte{}) && ordinal != 0 {
			return SourceID{Ordinal: ordinal}, nil
		}
	}
	return SourceID{}, fmt.Errorf("%w: malformed source identity", ErrInvalidRow)
}
