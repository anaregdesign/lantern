package graphstore

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

// Fixed-width metadata precedes the Vertex payload, which the application
// owns. Instants retain the complete time.Time seconds/nanoseconds range.
// A separate bit distinguishes no expiration from Unix epoch.
const metadataSize = 1 + 12 + 8 + 4 + 16

func appendMetadata(buf []byte, expiration time.Time, ts hlc.Timestamp) []byte {
	if expiration.IsZero() {
		buf = append(buf, make([]byte, 13)...)
	} else {
		buf = append(buf, 1)
		buf = binary.BigEndian.AppendUint64(buf, uint64(expiration.Unix()))
		buf = binary.BigEndian.AppendUint32(buf, uint32(expiration.Nanosecond()))
	}
	buf = binary.BigEndian.AppendUint64(buf, uint64(ts.WallNs))
	buf = binary.BigEndian.AppendUint32(buf, ts.Logical)
	return append(buf, ts.NodeID[:]...)
}

func parseMetadata(buf []byte) (time.Time, hlc.Timestamp, error) {
	if len(buf) < metadataSize {
		return time.Time{}, hlc.Timestamp{}, fmt.Errorf("%w: truncated metadata", ErrInvalidRow)
	}
	var expiry time.Time
	seconds := int64(binary.BigEndian.Uint64(buf[1:9]))
	ns := binary.BigEndian.Uint32(buf[9:13])
	switch buf[0] {
	case 0:
		if seconds != 0 || ns != 0 {
			return expiry, hlc.Timestamp{}, fmt.Errorf("%w: permanent row has deadline", ErrInvalidRow)
		}
	case 1:
		if ns >= 1e9 {
			return expiry, hlc.Timestamp{}, fmt.Errorf("%w: invalid nanoseconds", ErrInvalidRow)
		}
		expiry = time.Unix(seconds, int64(ns)).UTC()
		if expiry.IsZero() {
			return expiry, hlc.Timestamp{}, fmt.Errorf("%w: zero deadline marked expiring", ErrInvalidRow)
		}
	default:
		return expiry, hlc.Timestamp{}, fmt.Errorf("%w: unsupported deadline marker", ErrInvalidRow)
	}
	ts := hlc.Timestamp{WallNs: int64(binary.BigEndian.Uint64(buf[13:21])), Logical: binary.BigEndian.Uint32(buf[21:25]), NodeID: hlc.NodeID(buf[25:metadataSize])}
	return expiry, ts, nil
}

func (r *Rows[K, V]) encodeVertex(v Vertex[V]) ([]byte, error) {
	data, err := r.values.Encode(v.Value)
	if err != nil {
		return nil, err
	}
	buf := appendMetadata(make([]byte, 0, metadataSize+len(data)), v.Expiration, v.HLC)
	return append(buf, data...), nil
}
func (r *Rows[K, V]) decodeVertex(data []byte) (Vertex[V], error) {
	expiry, ts, err := parseMetadata(data)
	if err != nil {
		return Vertex[V]{}, err
	}
	value, err := r.values.Decode(data[metadataSize:])
	if err != nil {
		return Vertex[V]{}, fmt.Errorf("%w: decode vertex: %w", ErrInvalidRow, err)
	}
	return Vertex[V]{value, expiry, ts}, nil
}

const sourceSize = metadataSize + 4 + 1

func appendSource(buf []byte, s Source) []byte {
	buf = appendMetadata(buf, s.Expiration, s.HLC)
	buf = binary.BigEndian.AppendUint32(buf, math.Float32bits(s.Weight))
	if s.DerivedAggregate {
		return append(buf, 1)
	}
	return append(buf, 0)
}

func decodeSource(data []byte) (Source, error) {
	if len(data) != sourceSize || data[sourceSize-1] > 1 {
		return Source{}, fmt.Errorf("%w: invalid source", ErrInvalidRow)
	}
	expiry, ts, err := parseMetadata(data)
	if err != nil {
		return Source{}, err
	}
	return Source{Weight: math.Float32frombits(binary.BigEndian.Uint32(data[metadataSize:])), Expiration: expiry, HLC: ts, DerivedAggregate: data[sourceSize-1] == 1}, nil
}

func encodeEdge(e Edge) []byte {
	buf := appendMetadata(nil, time.Time{}, e.HLC)
	buf = binary.BigEndian.AppendUint64(buf, e.LastOrdinal)
	if e.Base != nil {
		buf = appendSource(buf, *e.Base)
	}
	return buf
}

func decodeEdge(data []byte) (Edge, error) {
	const noBaseSize = metadataSize + 8
	if len(data) != noBaseSize && len(data) != noBaseSize+sourceSize {
		return Edge{}, fmt.Errorf("%w: invalid edge size", ErrInvalidRow)
	}
	expires, ts, err := parseMetadata(data)
	if err != nil {
		return Edge{}, err
	}
	if !expires.IsZero() {
		return Edge{}, fmt.Errorf("%w: edge header has expiration", ErrInvalidRow)
	}
	e := Edge{HLC: ts, LastOrdinal: binary.BigEndian.Uint64(data[metadataSize:noBaseSize])}
	if len(data) > noBaseSize {
		base, err := decodeSource(data[noBaseSize:])
		if err != nil {
			return Edge{}, err
		}
		e.Base = &base
	}
	return e, nil
}
