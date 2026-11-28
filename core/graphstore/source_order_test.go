package graphstore

import (
	"bytes"
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestSourceOrderKeyMatchesSemanticTies(t *testing.T) {
	rows, err := NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
	if err != nil {
		t.Fatal(err)
	}
	key := EdgeKey[string]{Tail: "a\x00", Head: "b"}
	pair, err := rows.edgeTuple(1, sourceFamily, key)
	if err != nil {
		t.Fatal(err)
	}
	var previous []byte
	for _, row := range []struct {
		stamp hlc.Timestamp
		id    SourceID
	}{
		{hlc.Timestamp{WallNs: -1}, SourceID{ContribID: [24]byte{9}}},
		{hlc.Timestamp{}, SourceID{Ordinal: 1}},
		{hlc.Timestamp{}, SourceID{Ordinal: 4}},
		{hlc.Timestamp{}, SourceID{ContribID: [24]byte{1}}},
		{hlc.Timestamp{}, SourceID{ContribID: [24]byte{2}}},
		{hlc.Timestamp{NodeID: hlc.NodeID{1}}, SourceID{ContribID: [24]byte{3}}},
		{hlc.Timestamp{Logical: 1}, SourceID{ContribID: [24]byte{4}}},
	} {
		order, err := sourceOrderKey(pair, row.stamp, row.id)
		if err != nil || bytes.Compare(previous, order) >= 0 {
			t.Fatalf("ordered key = %x, %v, previous %x", order, err, previous)
		}
		canonical, err := rows.sourceKey(1, sourceFamily, key, row.id)
		if err != nil || len(order) > len(canonical)+sourceSize {
			t.Fatalf("derived key %d bytes exceeds charged canonical row %d bytes: %v", len(order), len(canonical)+sourceSize, err)
		}
		previous = order
	}
}

func TestSummarizeEdgeSourcesOmitsOnlyRequestedRows(t *testing.T) {
	ctx := t.Context()
	db, err := Open(ctx, t.TempDir(), hlc.NodeID{1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	rows, err := NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
	if err != nil {
		t.Fatal(err)
	}
	key := EdgeKey[string]{Tail: "dangling\x00tail", Head: "head"}
	now := time.Unix(100, 0).UTC()
	soon, later := now.Add(time.Minute), now.Add(time.Hour)
	edge := Edge{Base: &Source{Weight: 1 << 50, Expiration: soon}, LastOrdinal: 1}
	removed := SourceEntry{ID: SourceID{ContribID: [24]byte{1}}, Source: Source{Weight: 4}}
	live := SourceEntry{ID: SourceID{ContribID: [24]byte{2}}, Source: Source{Weight: 9, Expiration: later}}
	expired := SourceEntry{ID: SourceID{ContribID: [24]byte{3}}, Source: Source{Weight: 100, Expiration: now}}
	ordinal := SourceEntry{ID: SourceID{Ordinal: 1}, Source: Source{Weight: -(1 << 50)}}
	if err := db.UpdateRows(ctx, func(gen uint64, _ *tarp.Snapshot, b Writer) error {
		if err := rows.PutEdge(b, gen, key, edge); err != nil {
			return err
		}
		for _, entry := range []SourceEntry{expired, live, removed, ordinal} {
			if err := rows.ReplaceSource(b, gen, key, entry.ID, nil, &entry.Source); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	read := func() *ReadView {
		t.Helper()
		view, err := db.Read()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = view.Close() })
		return view
	}
	legacy := read()
	if _, err := rows.SummarizeEdgeSources(ctx, legacy.Snapshot, legacy.Generation, key, edge, now, nil); !errors.Is(err, ErrInvalidRow) {
		t.Fatalf("unverified source order: %v", err)
	}
	if err := db.UpdateRows(ctx, func(gen uint64, _ *tarp.Snapshot, b Writer) error {
		return rows.MarkSourceOrder(b, gen)
	}); err != nil {
		t.Fatal(err)
	}
	pinned := read()
	planned, err := rows.SummarizeEdgeSources(ctx, pinned.Snapshot, pinned.Generation, key, edge, now, map[SourceID]struct{}{removed.ID: {}})
	if err != nil {
		t.Fatal(err)
	}
	if planned.Key != key || planned.Edge != edge || planned.Weight != 9 || planned.Count != 3 || planned.RetainedAdds != 3 ||
		!planned.Explicit || !planned.Expired || !planned.Earliest.Equal(soon) || !planned.Latest.Equal(later) {
		t.Fatalf("live summary: %+v", planned)
	}
	if retained := planned.Retained; retained.Weight != 109 || retained.Count != 4 ||
		!retained.Explicit || !retained.Earliest.Equal(now) || !retained.Latest.Equal(later) {
		t.Fatalf("retained summary: %+v", retained)
	}
	if got, err := rows.EdgeAt(ctx, pinned.Snapshot, pinned.Generation, key, now); err != nil || got.Found {
		t.Fatalf("dangling edge exposed through public lookup: %+v, %v", got, err)
	}
	zero, err := rows.SummarizeEdgeSources(ctx, pinned.Snapshot, pinned.Generation, key, edge, now,
		map[SourceID]struct{}{removed.ID: {}, live.ID: {}, expired.ID: {}})
	if err != nil || zero.Weight != 0 || zero.Count != 2 || zero.Retained.Count != 2 || zero.Expired || zero.Explicit {
		t.Fatalf("surviving zero-weight bucket: %+v, %v", zero, err)
	}
	dead, err := rows.SummarizeEdgeSources(ctx, pinned.Snapshot, pinned.Generation, key, edge, later,
		map[SourceID]struct{}{removed.ID: {}, ordinal.ID: {}})
	if err != nil || dead.Count != 0 || dead.RetainedAdds != 2 || dead.Retained.Count != 3 || !dead.Expired || !dead.Retained.Explicit {
		t.Fatalf("expired rows must remain represented: %+v, %v", dead, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := rows.SummarizeEdgeSources(canceled, pinned.Snapshot, pinned.Generation, key, edge, now, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled summary: %v", err)
	}
	if err := db.UpdateRows(ctx, func(gen uint64, _ *tarp.Snapshot, b Writer) error {
		return rows.ReplaceSource(b, gen, key, removed.ID, &removed.Source, nil)
	}); err != nil {
		t.Fatal(err)
	}
	current := read()
	if got, err := rows.SummarizeEdgeSources(ctx, current.Snapshot, current.Generation, key, edge, now, nil); err != nil || got != planned {
		t.Fatalf("committed summary: %+v, %v, want %+v", got, err, planned)
	}
	if _, found, err := rows.GetSource(pinned.Snapshot, pinned.Generation, key, removed.ID); err != nil || !found {
		t.Fatalf("pinned cut lost the removed source: found=%t, err=%v", found, err)
	}
}

func TestSourceOrderFoldMatchesCanonicalFloat32AndReadBudget(t *testing.T) {
	ctx := t.Context()
	db, err := Open(ctx, t.TempDir(), hlc.NodeID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
	if err != nil {
		t.Fatal(err)
	}
	key := EdgeKey[string]{Tail: "a\x00", Head: "b"}
	nonfinite := EdgeKey[string]{Tail: "a\x00", Head: "infinite"}
	now := time.Unix(100, 0).UTC()
	later := now.Add(time.Hour)
	const count = 1536
	base := Source{Weight: 1 << 50}
	id := SourceID{ContribID: [24]byte{1}}
	before := Source{Weight: 7, HLC: hlc.Timestamp{WallNs: 1}, Expiration: later}
	if err := db.UpdateRows(ctx, func(gen uint64, _ *tarp.Snapshot, b Writer) error {
		return errors.Join(
			rows.PutVertex(b, gen, key.Tail, Vertex[string]{}),
			rows.PutVertex(b, gen, key.Head, Vertex[string]{}),
			rows.PutVertex(b, gen, nonfinite.Head, Vertex[string]{}),
			rows.PutEdge(b, gen, key, Edge{Base: &base, LastOrdinal: count}),
			rows.PutEdge(b, gen, nonfinite, Edge{Base: &Source{Weight: float32(math.Inf(1))}, LastOrdinal: 1}),
			rows.PutSource(b, gen, nonfinite, SourceID{Ordinal: 1}, Source{Weight: float32(math.Inf(-1))}),
			rows.PutSource(b, gen, key, SourceID{Ordinal: 1}, Source{Weight: -(1 << 50)}),
			rows.PutSource(b, gen, key, SourceID{Ordinal: 2}, Source{Weight: 9}),
			rows.PutSource(b, gen, key, SourceID{Ordinal: 3}, Source{Weight: 999, Expiration: now}),
			rows.PutSource(b, gen, key, SourceID{ContribID: [24]byte{2}}, Source{Weight: 3, HLC: hlc.Timestamp{WallNs: -1}}),
			rows.PutSource(b, gen, key, id, before),
		)
	}); err != nil {
		t.Fatal(err)
	}
	for first := uint64(4); first <= count; first += 256 {
		if err := db.UpdateRows(ctx, func(gen uint64, _ *tarp.Snapshot, b Writer) error {
			for i := min(count, first+255); i >= first; i-- {
				if err := rows.PutSource(b, gen, key, SourceID{Ordinal: i}, Source{}); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	read := func() *ReadView {
		t.Helper()
		cut, err := db.Read()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cut.Close() })
		return cut
	}
	fold := func(cut *ReadView, k EdgeKey[string]) (EdgeDetail, int, int) {
		t.Helper()
		var calls, bytes int
		detail, err := rows.EdgeAtWithReadCharge(ctx, cut.Snapshot, cut.Generation, k, now, func(n int) error {
			calls++
			bytes += n
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return detail, calls, bytes
	}
	legacy := read()
	if err := rows.EnsureSourceOrder(ctx, db); err != nil {
		t.Fatal(err)
	}
	ordered := read()
	if ordered.Generation != legacy.Generation || ordered.Revision != legacy.Revision {
		t.Fatal("building Source order changed the graph cut")
	}
	for _, k := range []EdgeKey[string]{key, nonfinite} {
		want, wantCalls, wantBytes := fold(legacy, k)
		got, gotCalls, gotBytes := fold(ordered, k)
		if got.Found != want.Found || math.Float32bits(got.Weight) != math.Float32bits(want.Weight) || got.Expiration != want.Expiration || gotCalls != wantCalls || gotBytes != wantBytes {
			t.Fatalf("%+v: ordered %v/%d/%d, legacy %v/%d/%d", k, got, gotCalls, gotBytes, want, wantCalls, wantBytes)
		}
	}
	if got, _, _ := fold(ordered, key); got.Weight != 16 || !got.Expiration.Equal(later) {
		t.Fatalf("canonical order = %+v", got)
	}
	for _, prefix := range []string{"", key.Tail} {
		count := 0
		for edge, err := range rows.FoldedEdges(ctx, ordered.Snapshot, ordered.Generation, prefix, now) {
			if err != nil {
				t.Fatal(err)
			}
			want, _, _ := fold(legacy, edge.Key)
			if math.Float32bits(edge.Detail.Weight) != math.Float32bits(want.Weight) {
				t.Fatalf("wide fold = %+v, want %+v", edge, want)
			}
			count++
		}
		if count != 2 {
			t.Fatalf("prefix %q returned %d Edges", prefix, count)
		}
	}
	if err := db.UpdateRows(ctx, func(gen uint64, _ *tarp.Snapshot, b Writer) error {
		after := Source{Weight: 20, HLC: hlc.Timestamp{WallNs: -2}}
		return rows.ReplaceSource(b, gen, key, id, &before, &after)
	}); err != nil {
		t.Fatal(err)
	}
	if err := rows.EnsureSourceOrder(ctx, db); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := fold(read(), key); got.Weight != 9 || !got.Expiration.IsZero() {
		t.Fatalf("replacement retained stale order = %+v", got)
	}
	if got, _, _ := fold(ordered, key); got.Weight != 16 {
		t.Fatalf("pinned order changed = %+v", got)
	}
}
