package graphstore

import (
	"context"
	"encoding/binary"
	"errors"
	"time"

	"github.com/anaregdesign/lantern/core/cache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

const (
	expiryChunkRows  = 128
	expiryChunkBytes = 1 << 20
)

// Visit Sources before Edge headers so finding any remaining Add in a bucket
// needs just one keys-only seek. Permanent causal barriers have no deadline.
var expiryFamilies = [...]byte{
	vertexMetadataFamily, sourceFamily, edgeFamily,
	byte(VertexTombstone), byte(EdgeTombstone), byte(ContributionTombstone),
}

type expiryCursor struct {
	generation uint64
	family     int
	after      []byte
}

// ReclaimExpired removes expired payloads and tombstones at a fixed recovery
// instant. The caller owns startup admission after validating the database.
// Every chunk reads the current generation and stages its changes from the
// same snapshot under the DB writer gate. Interruption can restart from the
// beginning because completed chunks are idempotent.
func (r *Rows[K, V]) ReclaimExpired(ctx context.Context, db *DB, now time.Time) error {
	var cursor expiryCursor
	for cursor.family < len(expiryFamilies) {
		next, err := r.reclaimExpiryNext(ctx, db, now, cursor)
		if err != nil {
			return err
		}
		cursor = next
	}
	return nil
}

func (r *Rows[K, V]) reclaimExpiryNext(ctx context.Context, db *DB, now time.Time, cursor expiryCursor) (expiryCursor, error) {
	var next expiryCursor
	err := db.UpdateRows(ctx, func(gen uint64, snap *tarp.Snapshot, b Writer) error {
		next = cursor
		if cursor.generation != gen {
			next = expiryCursor{generation: gen}
		}
		var reclaimedDeadline time.Time
		if err := r.reclaimExpiryChunk(ctx, snap, b, now, &next, &reclaimedDeadline); err != nil {
			return err
		}
		if !reclaimedDeadline.IsZero() {
			return StageVertexExpiryWatermark(ctx, snap, b, gen, reclaimedDeadline)
		}
		return nil
	})
	if err != nil {
		return cursor, err
	}
	return next, nil
}

func (r *Rows[K, V]) reclaimExpiryChunk(ctx context.Context, snap *tarp.Snapshot, b Writer, now time.Time, cursor *expiryCursor, reclaimedDeadline *time.Time) error {
	family := expiryFamilies[cursor.family]
	prefix, err := familyPrefix(cursor.generation, family)
	if err != nil {
		return err
	}
	bounds := prefixBounds(prefix)
	if cursor.after != nil {
		bounds = bounds.after(cursor.after, false)
	}
	var bytes int
	var rows int
	for row, err := range snap.ScanRange(ctx, bounds.lower, bounds.upper) {
		if err != nil {
			return err
		}
		size := len(row.Key) + len(row.Value)
		// Always admit one record so large but valid encoded keys make
		// progress. The enclosing Tarp batch still enforces record limits.
		if rows > 0 && bytes+size > expiryChunkBytes {
			return nil
		}
		if err := r.reclaimExpiryRow(ctx, snap, b, now, cursor.generation, family, row, reclaimedDeadline); err != nil {
			return err
		}
		cursor.after = row.Key
		bytes += size
		rows++
		if rows >= expiryChunkRows || bytes >= expiryChunkBytes {
			return nil
		}
	}
	cursor.family++
	cursor.after = nil
	return nil
}

func (r *Rows[K, V]) reclaimExpiryRow(ctx context.Context, snap *tarp.Snapshot, b Writer, now time.Time, gen uint64, family byte, row tarp.Entry, reclaimedDeadline *time.Time) error {
	switch family {
	case vertexMetadataFamily:
		expiry, ts, err := parseVertexProjection(row.Value)
		if err != nil || cache.IsLiveAt(expiry, now) {
			return err
		}
		key, err := r.decodeVertexKey(row.Key)
		if err != nil {
			return err
		}
		if err := r.preserveExpiryBarrier(snap, b, gen, FloorKey[K]{Kind: VertexBarrier, Key: key}, ts); err != nil {
			return err
		}
		if expiry.After(*reclaimedDeadline) {
			*reclaimedDeadline = expiry
		}
		return r.DeleteVertex(b, gen, key)
	case sourceFamily:
		source, err := decodeAdd(row.Value)
		if err != nil {
			return err
		}
		if !cache.IsLiveAt(source.Expiration, now) {
			tuple, err := sourceTupleCodec.Decode(row.Key)
			if err != nil {
				return err
			}
			id, err := sourceIdentity(tuple)
			if err != nil {
				return err
			}
			return replaceSource(b, tuple, id, &source, nil)
		}
	case edgeFamily:
		return r.reclaimExpiredEdge(ctx, snap, b, now, gen, row)
	default:
		floor, err := decodeFloor(FloorKind(family), row.Value)
		if err != nil {
			return err
		}
		if !cache.IsLiveAt(floor.Expiration, now) {
			b.Delete(row.Key)
		}
	}
	return nil
}

func (r *Rows[K, V]) reclaimExpiredEdge(ctx context.Context, snap *tarp.Snapshot, b Writer, now time.Time, gen uint64, row tarp.Entry) error {
	edge, err := decodeEdge(row.Value)
	if err != nil || edge.Base != nil && cache.IsLiveAt(edge.Base.Expiration, now) {
		return err
	}
	tuple, err := edgeTupleCodec.Decode(row.Key)
	if err != nil {
		return err
	}
	key, err := r.decodeEdgeKey(tuple)
	if err != nil {
		return err
	}
	bounds, err := r.sourceBounds(gen, key)
	if err != nil {
		return err
	}
	for _, err := range snap.KeysRange(ctx, bounds.lower, bounds.upper) {
		if err != nil {
			return err
		}
		if edge.Base != nil {
			edge.Base = nil
			return r.PutEdge(b, gen, key, edge)
		}
		return nil
	}
	if err := r.preserveExpiryBarrier(snap, b, gen, FloorKey[K]{Kind: EdgeBarrier, Tail: key.Tail, Head: key.Head}, edge.HLC); err != nil {
		return err
	}
	return r.DeleteEdge(b, gen, key)
}

func (r *Rows[K, V]) preserveExpiryBarrier(snap *tarp.Snapshot, b Writer, gen uint64, key FloorKey[K], ts hlc.Timestamp) error {
	if ts == (hlc.Timestamp{}) {
		return nil
	}
	old, found, err := r.GetFloor(snap, gen, key)
	if err != nil || found && !old.HLC.Less(ts) {
		return err
	}
	return r.PutFloor(b, gen, key, Floor{HLC: ts})
}

// VertexExpiryWatermark is the latest deadline of a physically reclaimed
// Vertex. Derived views cannot resurrect reclaimed rows
// when their wall clock steps back before this cut.
func VertexExpiryWatermark(ctx context.Context, snap *tarp.Snapshot, generation uint64) (time.Time, error) {
	key, err := familyPrefix(generation, vertexExpiryFamily)
	if err != nil {
		return time.Time{}, err
	}
	value, err := snap.GetContext(ctx, key)
	if errors.Is(err, tarp.ErrNotFound) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	if len(value) != 12 || binary.BigEndian.Uint32(value[8:]) >= 1e9 {
		return time.Time{}, ErrInvalidRow
	}
	return time.Unix(int64(binary.BigEndian.Uint64(value[:8])), int64(binary.BigEndian.Uint32(value[8:]))).UTC(), nil
}

// StageVertexExpiryWatermark advances the watermark in the same batch as
// physically reclaimed Vertex rows.
func StageVertexExpiryWatermark(ctx context.Context, snap *tarp.Snapshot, writer Writer, generation uint64, now time.Time) error {
	previous, err := VertexExpiryWatermark(ctx, snap, generation)
	if err != nil || !now.After(previous) {
		return err
	}
	key, err := familyPrefix(generation, vertexExpiryFamily)
	if err != nil {
		return err
	}
	value := binary.BigEndian.AppendUint64(nil, uint64(now.Unix()))
	writer.Put(key, binary.BigEndian.AppendUint32(value, uint32(now.Nanosecond())))
	return nil
}
