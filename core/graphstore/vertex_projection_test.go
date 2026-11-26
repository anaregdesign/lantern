package graphstore

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestVertexProjectionTracksExpiryAndValidatesReopen(t *testing.T) {
	db, err := tarp.Open(t.TempDir(), tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(0, 0).UTC()
	ts := hlc.Timestamp{WallNs: 10}
	want := []VertexEntry[string, string]{
		{"a", Vertex[string]{Value: "body", HLC: ts, Expiration: now.Add(time.Hour)}},
		{"a\x00", Vertex[string]{Value: "permanent"}},
		{"b", Vertex[string]{Expiration: now}},
	}
	var b tarp.WriteBatch
	for _, entry := range slices.Backward(want) {
		if err := rows.PutVertex(&b, 1, entry.Key, entry.Vertex); err != nil {
			t.Fatal(err)
		}
	}
	if err := rows.PutVertex(&b, 2, "a", Vertex[string]{Value: "other generation"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Apply(&b); err != nil {
		t.Fatal(err)
	}
	pinned, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Close()
	checkKeys := func(s *tarp.Snapshot, want []string) {
		t.Helper()
		var n int
		for entry, err := range rows.LiveVertexRange(t.Context(), s, 1, "", "", nil, false, true, now) {
			if err != nil || n >= len(want) || entry.Key != want[n] || entry.Vertex.Value != "" {
				t.Fatalf("projected row %d = %+v, err=%v", n, entry, err)
			}
			n++
		}
		if n != len(want) {
			t.Fatalf("got %d keys, want %d", n, len(want))
		}
	}
	checkKeys(pinned, []string{"a", "a\x00"})
	b.Reset()
	if err := errors.Join(rows.PutVertex(&b, 1, "a", Vertex[string]{Expiration: now}), rows.DeleteVertex(&b, 1, "a\x00"), rows.PutVertex(&b, 1, "b", Vertex[string]{Value: "revived"})); err != nil {
		t.Fatal(err)
	}
	if err := db.Apply(&b); err != nil {
		t.Fatal(err)
	}
	next, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	checkKeys(pinned, []string{"a", "a\x00"})
	checkKeys(next, []string{"b"})
	var verified []VertexEntry[string, string]
	for entry, err := range rows.VerifiedVertices(t.Context(), pinned, 1) {
		if err != nil {
			t.Fatal(err)
		}
		verified = append(verified, entry)
	}
	if !slices.Equal(verified, want) {
		t.Fatalf("verified rows at pinned cut = %+v, want %+v", verified, want)
	}
	var count int
	for _, err := range rows.VerifiedVertices(t.Context(), next, 1) {
		if err != nil {
			t.Fatal(err)
		}
		count++
	}
	if count != 2 {
		t.Fatalf("verified %d physical rows", count)
	}
	projected, err := rows.vertexKey(1, vertexMetadataFamily, "b")
	if err != nil {
		t.Fatal(err)
	}
	b.Reset()
	b.Put(projected, appendMetadata(nil, now, ts))
	if err := db.Apply(&b); err != nil {
		t.Fatal(err)
	}
	bad, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()
	var got error
	for _, err := range rows.VerifiedVertices(t.Context(), bad, 1) {
		if err != nil {
			got = err
		}
	}
	if !errors.Is(got, ErrInvalidRow) {
		t.Fatalf("mismatched projection error = %v", got)
	}
}

func TestVerifiedVertexBytesRejectsUnknownGraphFamilies(t *testing.T) {
	db, err := tarp.Open(t.TempDir(), tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
	if err != nil {
		t.Fatal(err)
	}
	for i, family := range []byte{0, vertexMetadataFamily + 1} {
		gen := uint64(i + 1)
		key, err := familyPrefix(gen, family)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Put(key, []byte("unrecognized")); err != nil {
			t.Fatal(err)
		}
		s, err := db.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		var got error
		for _, err := range rows.VerifiedVertexBytes(t.Context(), s, gen) {
			got = err
		}
		if err := s.Close(); err != nil || !errors.Is(got, ErrInvalidRow) {
			t.Fatalf("family %d: verification %v, close %v", family, got, err)
		}
	}
}
