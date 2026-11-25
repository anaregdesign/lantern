package graphstore

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/anaregdesign/tarp"
)

func TestRowScanLexicalPrefixCursor(t *testing.T) {
	r, err := NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
	if err != nil {
		t.Fatal(err)
	}
	db, err := tarp.Open(t.TempDir(), tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	keys := []string{"a", "a\x00", "ab", "b", "東", "東京"}
	var batch tarp.WriteBatch
	for _, tail := range keys {
		if err := r.PutVertex(&batch, 7, tail, Vertex[string]{Value: tail}); err != nil {
			t.Fatal(err)
		}
		for _, head := range keys {
			if err := r.PutEdge(&batch, 7, EdgeKey[string]{tail, head}, Edge{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := db.Apply(&batch); err != nil {
		t.Fatal(err)
	}
	snap, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	for _, reverse := range []bool{false, true} {
		for _, prefix := range []string{"", "a", "a\x00", "東", "東京", "x"} {
			var want []string
			for _, key := range keys {
				if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
					want = append(want, key)
				}
			}
			if reverse {
				slices.Reverse(want)
			}
			var got []string
			var after *string
			for {
				hit := false
				for entry, err := range r.Vertices(t.Context(), snap, 7, prefix, after, reverse) {
					if err != nil {
						t.Fatal(err)
					}
					got = append(got, entry.Key)
					hit = true
					after = &entry.Key
					break
				}
				if !hit {
					break
				}
			}
			if !slices.Equal(want, got) {
				t.Fatalf("prefix %q reverse=%t got %q want %q", prefix, reverse, got, want)
			}
		}
		want := []EdgeKey[string]{{"a", "東"}, {"a", "東京"}, {"a\x00", "東"}, {"a\x00", "東京"}, {"ab", "東"}, {"ab", "東京"}}
		if reverse {
			slices.Reverse(want)
		}
		for i := 0; i <= len(want); i++ {
			var after *EdgeKey[string]
			if i > 0 {
				after = &want[i-1]
			}
			var got []EdgeKey[string]
			for entry, err := range r.Edges(t.Context(), snap, 7, "a", "東", after, reverse) {
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, entry.Key)
			}
			if !slices.Equal(got, want[i:]) {
				t.Fatalf("reverse=%t after=%v got %#v want %#v", reverse, after, got, want[i:])
			}
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, err := range r.Edges(ctx, snap, 7, "", "a", nil, false) {
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		return
	}
	t.Fatal("cancelled scan silently completed")
}
