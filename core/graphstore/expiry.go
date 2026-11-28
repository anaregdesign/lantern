package graphstore

import (
	"bytes"
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

// ExpiryCursor resumes an ordered scan. Its zero value starts a pass.
// A cursor is ephemeral and restarts at the beginning after a generation switch.
type ExpiryCursor struct {
	generation uint64
	family     int
	after      []byte
}

// Done reports completion of the current pass. Use a zero cursor to start another.
func (c ExpiryCursor) Done() bool { return c.family == len(expiryFamilies) }

// Generation identifies the graph generation visited by this cursor.
func (c ExpiryCursor) Generation() uint64 { return c.generation }

// ExpiryPlan holds at most one chunk of candidate rows found on a read cut.
// Only ReclaimExpiredChunk may advance the cursor for a plan with candidates.
type ExpiryPlan struct {
	db      *DB
	start   ExpiryCursor
	next    ExpiryCursor
	rows    []tarp.Entry
	family  byte
	scanned int
}

func (p ExpiryPlan) Pending() bool { return len(p.rows) != 0 }

// Next returns progress from an empty plan or the original cursor while pending.
func (p ExpiryPlan) Next() ExpiryCursor {
	if p.Pending() {
		return p.start
	}
	return p.next
}
func (p ExpiryPlan) ScannedEdges() int {
	if p.family == edgeFamily {
		return p.scanned
	}
	return 0
}
func (p ExpiryPlan) ScannedSources() int {
	if p.family == sourceFamily {
		return p.scanned
	}
	return 0
}

// ExpiryEffects describes exact preimages of rows changed by a durable chunk.
// A deleted Vertex or Edge header retains its nonzero HLC as a causal barrier.
type ExpiryEffects[K comparable] struct {
	Vertices []VertexExpiry[K]
	Sources  []SourceExpiry[K]
	Edges    []EdgeExpiry[K]
	Floors   []FloorEntry[K]
}

type VertexExpiry[K comparable] struct {
	Key      K
	Metadata VertexMetadata
}

type SourceExpiry[K comparable] struct {
	Key    EdgeKey[K]
	Source SourceEntry
}

type EdgeExpiry[K comparable] struct {
	Key     EdgeKey[K]
	Before  Edge
	Removed bool // when false, only the Base was cleared
}

// ReclaimExpired removes expired payloads and tombstones at a fixed recovery
// instant. The caller owns startup admission after validating the database.
// Interruption can restart from the beginning because chunks are idempotent.
func (r *Rows[K, V]) ReclaimExpired(ctx context.Context, db *DB, now time.Time) error {
	var cursor ExpiryCursor
	for !cursor.Done() {
		plan, err := r.DiscoverExpiryChunk(ctx, db, now, cursor)
		if err != nil {
			return err
		}
		cursor, _, err = r.ReclaimExpiredChunk(ctx, db, now, plan)
		if err != nil {
			return err
		}
	}
	return nil
}

// DiscoverExpiryChunk reads one bounded chunk on the active snapshot without
// taking the writer gate.
func (r *Rows[K, V]) DiscoverExpiryChunk(ctx context.Context, db *DB, now time.Time, cursor ExpiryCursor) (ExpiryPlan, error) {
	plan := ExpiryPlan{db: db, start: cursor, next: cursor}
	if err := ctx.Err(); err != nil {
		return plan, err
	}
	err := db.WithCommittedSnapshot(func(gen uint64, snap *tarp.Snapshot) error {
		if cursor.generation != gen {
			plan.next = ExpiryCursor{generation: gen}
		}
		if plan.next.Done() {
			return nil
		}
		return r.discoverExpiryRows(ctx, snap, now, &plan)
	})
	if err != nil {
		return ExpiryPlan{start: cursor, next: cursor}, err
	}
	return plan, nil
}

func (r *Rows[K, V]) discoverExpiryRows(ctx context.Context, snap *tarp.Snapshot, now time.Time, plan *ExpiryPlan) error {
	cursor := &plan.next
	family := expiryFamilies[cursor.family]
	plan.family = family
	prefix, err := familyPrefix(cursor.generation, family)
	if err != nil {
		return err
	}
	bounds := prefixBounds(prefix)
	if cursor.after != nil {
		bounds = bounds.after(cursor.after, false)
	}
	var scannedBytes int
	for row, err := range snap.ScanRange(ctx, bounds.lower, bounds.upper) {
		if err != nil {
			return err
		}
		size := len(row.Key) + len(row.Value)
		// Always admit one record so large but valid keys make progress.
		if plan.scanned > 0 && scannedBytes+size > expiryChunkBytes {
			return nil
		}
		due, err := r.expiryRowDue(ctx, snap, now, cursor.generation, family, row)
		if err != nil {
			return err
		}
		if due {
			plan.rows = append(plan.rows, row)
		}
		cursor.after = row.Key
		scannedBytes += size
		plan.scanned++
		if plan.scanned >= expiryChunkRows || scannedBytes >= expiryChunkBytes {
			return nil
		}
	}
	cursor.family++
	cursor.after = nil
	return nil
}

func (r *Rows[K, V]) expiryRowDue(ctx context.Context, snap *tarp.Snapshot, now time.Time, gen uint64, family byte, row tarp.Entry) (bool, error) {
	switch family {
	case vertexMetadataFamily:
		expiry, _, err := parseVertexProjection(row.Value)
		return !cache.IsLiveAt(expiry, now), err
	case sourceFamily:
		source, err := decodeAdd(row.Value)
		return !cache.IsLiveAt(source.Expiration, now), err
	case edgeFamily:
		edge, err := decodeEdge(row.Value)
		if err != nil {
			return false, err
		}
		if edge.Base != nil {
			return !cache.IsLiveAt(edge.Base.Expiration, now), nil
		}
		key, err := r.expiryEdgeKey(row.Key)
		if err != nil {
			return false, err
		}
		hasSources, err := r.expiryEdgeHasSources(ctx, snap, gen, key)
		return !hasSources, err
	default:
		floor, err := decodeFloor(FloorKind(family), row.Value)
		return !cache.IsLiveAt(floor.Expiration, now), err
	}
}

// ReclaimExpiredChunk rechecks one plan on the latest writer cut. A missing,
// replaced or renewed row is left alone. A generation change restarts the
// cursor without writing. Effects and cursor progress are returned only on
// success. Empty plans do not acquire the writer gate.
func (r *Rows[K, V]) ReclaimExpiredChunk(ctx context.Context, db *DB, now time.Time, plan ExpiryPlan) (ExpiryCursor, ExpiryEffects[K], error) {
	if !plan.Pending() {
		return plan.next, ExpiryEffects[K]{}, nil
	}
	if plan.db != db {
		return plan.start, ExpiryEffects[K]{}, ErrInvalidRow
	}
	var next ExpiryCursor
	var effects ExpiryEffects[K]
	err := db.UpdateRows(ctx, func(gen uint64, snap *tarp.Snapshot, b Writer) error {
		effects = ExpiryEffects[K]{}
		next = plan.next
		if gen != plan.next.generation {
			next = ExpiryCursor{generation: gen}
			return nil
		}
		var reclaimedDeadline time.Time
		for _, row := range plan.rows {
			value, err := snap.GetContext(ctx, row.Key)
			if errors.Is(err, tarp.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if !bytes.Equal(value, row.Value) {
				continue
			}
			if err := r.reclaimExpiryRow(ctx, snap, b, now, gen, plan.family, row, &reclaimedDeadline, &effects); err != nil {
				return err
			}
		}
		if !reclaimedDeadline.IsZero() {
			return StageVertexExpiryWatermark(ctx, snap, b, gen, reclaimedDeadline)
		}
		return nil
	})
	if err != nil {
		return plan.start, ExpiryEffects[K]{}, err
	}
	return next, effects, nil
}

func (r *Rows[K, V]) reclaimExpiryRow(ctx context.Context, snap *tarp.Snapshot, b Writer, now time.Time, gen uint64, family byte, row tarp.Entry, reclaimedDeadline *time.Time, effects *ExpiryEffects[K]) error {
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
		effects.Vertices = append(effects.Vertices, VertexExpiry[K]{Key: key, Metadata: VertexMetadata{Expiration: expiry, HLC: ts}})
		return r.DeleteVertex(b, gen, key)
	case sourceFamily:
		source, err := decodeAdd(row.Value)
		if err != nil || cache.IsLiveAt(source.Expiration, now) {
			return err
		}
		tuple, err := sourceTupleCodec.Decode(row.Key)
		if err != nil {
			return err
		}
		id, err := sourceIdentity(tuple)
		if err != nil {
			return err
		}
		key, err := r.decodeEdgeKey(tuple.Edge)
		if err != nil {
			return err
		}
		effects.Sources = append(effects.Sources, SourceExpiry[K]{Key: key, Source: SourceEntry{ID: id, Source: source}})
		return replaceSource(b, tuple, id, &source, nil)
	case edgeFamily:
		return r.reclaimExpiredEdge(ctx, snap, b, now, gen, row, effects)
	default:
		kind := FloorKind(family)
		floor, err := decodeFloor(kind, row.Value)
		if err != nil || cache.IsLiveAt(floor.Expiration, now) {
			return err
		}
		key, err := r.decodeFloorKey(kind, row.Key)
		if err != nil {
			return err
		}
		effects.Floors = append(effects.Floors, FloorEntry[K]{Key: key, Floor: floor})
		b.Delete(row.Key)
		return nil
	}
}

func (r *Rows[K, V]) reclaimExpiredEdge(ctx context.Context, snap *tarp.Snapshot, b Writer, now time.Time, gen uint64, row tarp.Entry, effects *ExpiryEffects[K]) error {
	edge, err := decodeEdge(row.Value)
	if err != nil || edge.Base != nil && cache.IsLiveAt(edge.Base.Expiration, now) {
		return err
	}
	key, err := r.expiryEdgeKey(row.Key)
	if err != nil {
		return err
	}
	hasSources, err := r.expiryEdgeHasSources(ctx, snap, gen, key)
	if err != nil {
		return err
	}
	if hasSources {
		if edge.Base == nil {
			return nil
		}
		effects.Edges = append(effects.Edges, EdgeExpiry[K]{Key: key, Before: edge})
		edge.Base = nil
		return r.PutEdge(b, gen, key, edge)
	}
	if err := r.preserveExpiryBarrier(snap, b, gen, FloorKey[K]{Kind: EdgeBarrier, Tail: key.Tail, Head: key.Head}, edge.HLC); err != nil {
		return err
	}
	effects.Edges = append(effects.Edges, EdgeExpiry[K]{Key: key, Before: edge, Removed: true})
	return r.DeleteEdge(b, gen, key)
}

func (r *Rows[K, V]) expiryEdgeKey(raw []byte) (EdgeKey[K], error) {
	tuple, err := edgeTupleCodec.Decode(raw)
	if err != nil {
		return EdgeKey[K]{}, err
	}
	return r.decodeEdgeKey(tuple)
}

func (r *Rows[K, V]) expiryEdgeHasSources(ctx context.Context, snap *tarp.Snapshot, gen uint64, key EdgeKey[K]) (bool, error) {
	bounds, err := r.sourceBounds(gen, key)
	if err != nil {
		return false, err
	}
	for _, err := range snap.KeysRange(ctx, bounds.lower, bounds.upper) {
		return err == nil, err
	}
	return false, nil
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
