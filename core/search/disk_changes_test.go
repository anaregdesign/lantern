package search

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/tarp"
)

func TestDiskSearchChangedPostimagesMatchDirectIndex(t *testing.T) {
	ctx := t.Context()
	source := diskTestSource()
	now := source.Watermark.Add(time.Second)
	base := []DiskDocument{
		{Key: "a/drop", Document: Fields{{FieldKey, "engine"}, {FieldValue, "wrong language"}}},
		{Key: "a/expiry", Document: Text("danger expired engine"), Expiration: now},
		{Key: "a/rename", Document: Text("lantern dangerous"), Version: []byte{4}},
		{Key: "a/renew", Document: Text("lantern engine"), Expiration: now},
		{Key: "b/base", Document: Fields{{FieldValue, "lantern engine engine"}, {FieldValue, "Tokyo"}}},
		{Key: "b/ttl", Document: Text("lantern nuisance"), Expiration: now},
	}
	changes := []DiskDocument{
		{Key: "a/drop"},
		{Key: "a/expiry", Document: Text("still born expired engine"), Expiration: now},
		{Key: "a/rename", Document: Fields{{FieldKey, "lantern search"}, {FieldValue, "stable engine lantern"}}, Version: []byte{5}},
		{Key: "a/renew", Document: Text("lantern engine"), Expiration: now.Add(time.Hour)},
		{Key: "c/new", Document: Fields{{FieldValue, "lantern engineering 東京駅"}, {FieldValue, "lantern engine"}}},
		{Key: "c/sole", Document: Text("unique newful"), Expiration: now.Add(time.Hour)},
	}
	current := []DiskDocument{changes[2], changes[3], base[4], changes[4], changes[5]}
	analyzer := NewScriptAwareAnalyzer()
	options := []IndexOption{WithPositions(), WithIndexClock(func() time.Time { return now })}
	ram := NewInvertedIndex[string, Document](analyzer, nil, strings.Compare, options...)
	for _, doc := range current {
		if err := ram.IndexWithExpiration(doc.Key, doc.Document, doc.Expiration); err != nil {
			t.Fatal(err)
		}
	}
	db, err := tarp.Open(t.TempDir(), tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := BuildDiskBase(ctx, db, 1, source, diskTestDocuments(base), analyzer, options...); err != nil {
		t.Fatal(err)
	}
	index, err := OpenDiskIndex(ctx, db, 1, source, analyzer, nil, options...)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query  string
		opts   MatchOptions
		phrase bool
	}{
		{query: "lantern engine"},
		{query: "lantern engine", opts: MatchOptions{Mode: MatchAll}},
		{query: "lantern engine", phrase: true},
		{query: "engin", opts: MatchOptions{PrefixTerms: true}},
		{query: "enginer", opts: MatchOptions{Fuzziness: 1}},
		{query: "engine newful", opts: MatchOptions{Mode: MatchMinShould, MinShouldMatch: 1}},
		{query: "newful"},
		{query: "東京駅", phrase: true},
		{query: "dang", opts: MatchOptions{PrefixTerms: true}},
	} {
		for _, accept := range []func(string) bool{nil, func(key string) bool { return !strings.HasPrefix(key, "a/") }} {
			var want []Result[string]
			if tc.phrase {
				want, _, err = ram.SearchPhraseTopKContextAt(ctx, tc.query, 4, accept, Budget{}, now)
			} else {
				want, _, err = ram.SearchMatchTopKContextAt(ctx, tc.query, 4, accept, tc.opts, Budget{}, now)
			}
			if err != nil {
				t.Fatal(err)
			}
			got, stats, err := index.SearchAtWithChanges(ctx, tc.query, 4, accept, tc.opts, tc.phrase, Budget{}, now, diskTestDocuments(changes))
			if err != nil || len(got) != len(want) || stats.ChangeVisits != int64(len(changes)) {
				t.Fatalf("query %q: got %v, %v, changed %d; want %v", tc.query, got, err, stats.ChangeVisits, want)
			}
			for i := range want {
				if got[i].ID != want[i].ID || math.Float64bits(got[i].Score) != math.Float64bits(want[i].Score) {
					t.Fatalf("query %q: got %v, want %v", tc.query, got, want)
				}
			}
		}
	}
	got, _, err := index.SearchAtWithChanges(ctx, "engine", 4, nil, MatchOptions{}, false, Budget{MaxChangeVisits: 1}, now, diskTestDocuments(changes))
	if got != nil || !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("change budget returned %v, %v", got, err)
	}
	duplicated := []DiskDocument{changes[0], changes[0]}
	got, _, err = index.SearchAtWithChanges(ctx, "engine", 4, nil, MatchOptions{}, false, Budget{}, now, diskTestDocuments(duplicated))
	if got != nil || !errors.Is(err, ErrDiskIndex) {
		t.Fatalf("duplicated change returned %v, %v", got, err)
	}
}
