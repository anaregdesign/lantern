package search

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/tarp"
)

func TestDiskSearchMatchesReopenedRAMOracle(t *testing.T) {
	ctx := t.Context()
	source := DiskSource{Store: "node1", Corpus: "test", Fingerprint: "analyzer+scorer/v1", Epoch: 3, Sequence: 7, Watermark: time.Unix(100, 0).UTC()}
	analyzer := NewScriptAwareAnalyzer()
	scorer := FieldWeighted{Base: ClassWeighted{GramWeight: DefaultGramWeight}, KeyWeight: DefaultKeyFieldWeight}
	inputs := []DiskDocument{
		{Key: "a/expired", Document: Text("lantern storage"), Expiration: source.Watermark},
		{Key: "a/near", Document: Fields{{FieldKey, "lantern graph"}, {FieldValue, "lantern storage engine lantern"}}},
		{Key: "b/far", Document: Fields{{FieldKey, "lantern"}, {FieldValue, "lantern durable portable storage"}}},
		{Key: "b/long", Document: Fields{{FieldValue, strings.Repeat("harbor ", 2050) + "storage"}}},
		{Key: "b/renewed", Document: Text("storage lantern"), Expiration: source.Watermark.Add(time.Minute)},
		{Key: "b/split", Document: Fields{{FieldValue, "lantern"}, {FieldValue, "storage"}}},
		{Key: "c/japanese", Document: Text("東京駅周辺の検索 東京駅"), Version: []byte{0, 1, 2}},
		{Key: "c/japanese2", Document: Text("東京の駅を検索")},
		{Key: "c/term", Document: Text("language storage harbour")},
	}
	for i := range 520 {
		inputs = append(inputs, DiskDocument{Key: fmt.Sprintf("bulk/%04d", i), Document: Text("lantern bulk storage engine graph")})
	}
	// Build input is an ordered key stream. Some document IDs deliberately do
	// not become postings or share a field instance with a matching term.
	sorted := append([]DiskDocument(nil), inputs[:6]...)
	sorted = append(sorted, inputs[9:]...)
	sorted = append(sorted, inputs[6:9]...)
	ram := NewInvertedIndex[string, Document](analyzer, scorer, strings.Compare, WithPositions(), WithIndexClock(func() time.Time { return source.Watermark }))
	for _, input := range sorted {
		prepared, _, err := ram.Prepare(input.Document)
		if err != nil {
			t.Fatal(err)
		}
		if expirationLiveAt(input.Expiration, source.Watermark) {
			if err := ram.IndexPreparedWithExpiration(input.Key, prepared, input.Expiration); err != nil {
				t.Fatal(err)
			}
		}
	}
	dir := t.TempDir()
	db, err := tarp.Open(dir, tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := BuildDiskBase(ctx, db, 1, source, diskTestDocuments(sorted), analyzer, WithPositions()); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = tarp.Open(dir, tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	disk, err := OpenDiskIndex(ctx, db, 1, source, analyzer, scorer, WithPositions())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		query  string
		mode   MatchOptions
		phrase bool
	}{
		{query: "lantern storage"},
		{query: "lantern storage nonce", mode: MatchOptions{Mode: MatchAll}},
		{query: "lantern storage", mode: MatchOptions{Mode: MatchAll}},
		{query: "lantern storage nonce", mode: MatchOptions{Mode: MatchMinShould, MinShouldMatch: 2}},
		{query: "lan storage", mode: MatchOptions{PrefixTerms: true}},
		{query: "lantarn storage", mode: MatchOptions{Fuzziness: 1}},
		{query: "lantaren storage", mode: MatchOptions{Fuzziness: 2, PrefixTerms: true}},
		{query: "lantern storage", phrase: true},
		{query: "harbor harbor", phrase: true},
		{query: "harbor storage", phrase: true},
		{query: "東京駅", phrase: true},
		{query: "東京駅", mode: MatchOptions{PrefixTerms: true, Fuzziness: 2}},
		{query: "unknown_word"},
	}
	filtered := func(key string) bool { return !strings.HasPrefix(key, "a/") && !strings.HasPrefix(key, "bulk/") }
	for i, tc := range cases {
		t.Run(fmt.Sprintf("%d-%s", i, tc.query), func(t *testing.T) {
			for _, accept := range []func(string) bool{nil, filtered} {
				var want []Result[string]
				var err error
				if tc.phrase {
					want, _, err = ram.SearchPhraseTopKContextAt(ctx, tc.query, 8, accept, Budget{}, source.Watermark)
				} else {
					want, _, err = ram.SearchMatchTopKContextAt(ctx, tc.query, 8, accept, tc.mode, Budget{}, source.Watermark)
				}
				if err != nil {
					t.Fatal(err)
				}
				got, stats, err := disk.Search(ctx, tc.query, 8, accept, tc.mode, tc.phrase, Budget{})
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != len(want) || stats.DiskReadBytes == 0 {
					t.Fatalf("disk = %#v with %d bytes, RAM = %#v", got, stats.DiskReadBytes, want)
				}
				for j := range want {
					if got[j].ID != want[j].ID || math.Float64bits(got[j].Score) != math.Float64bits(want[j].Score) {
						t.Fatalf("disk rank = %#v, RAM = %#v", got, want)
					}
				}
			}
		})
	}
	if _, _, err = disk.SearchAt(ctx, "lantern", 5, nil, MatchOptions{}, false, Budget{}, source.Watermark.Add(-time.Nanosecond)); !errors.Is(err, ErrIndexIncomplete) {
		t.Fatalf("prior liveness instant must fence immutable base: %v", err)
	}
	got, _, err := disk.Search(ctx, "lantern", 5, nil, MatchOptions{}, false, Budget{MaxDiskReadBytes: 1})
	if !errors.Is(err, ErrBudgetExceeded) || got != nil {
		t.Fatalf("disk budget returned %v, %v", got, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if got, _, err := disk.Search(canceled, "lantern", 5, nil, MatchOptions{}, false, Budget{}); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("cancelled search = %v, %v", got, err)
	}
}

func TestDiskSearchMissingAndMalformedPosting(t *testing.T) {
	ctx := t.Context()
	for _, malformed := range []bool{false, true} {
		t.Run(fmt.Sprint(malformed), func(t *testing.T) {
			db, err := tarp.Open(t.TempDir(), tarp.Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			source := diskTestSource()
			analyzer := NewScriptAwareAnalyzer()
			docs := []DiskDocument{{Key: "hit", Document: Text("plain")}}
			if err := BuildDiskBase(ctx, db, 1, source, diskTestDocuments(docs), analyzer); err != nil {
				t.Fatal(err)
			}
			idx, err := OpenDiskIndex(ctx, db, 1, source, analyzer, nil)
			if err != nil {
				t.Fatal(err)
			}
			key := diskKey(1, diskPostingRow, uint8(ClassWord), "plain", uint64(1))
			if malformed {
				err = db.Put(key, []byte{1, 1, 1})
			} else {
				err = db.Delete(key)
			}
			if err != nil {
				t.Fatal(err)
			}
			got, _, err := idx.Search(ctx, "plain", 5, nil, MatchOptions{}, false, Budget{})
			if !errors.Is(err, ErrDiskIndex) || got != nil {
				t.Fatalf("damaged index returned %v, %v", got, err)
			}
		})
	}
}

func diskTestSource() DiskSource {
	return DiskSource{Store: "node1", Corpus: "search", Fingerprint: "v1", Epoch: 1,
		Watermark: time.Unix(1000, 0).UTC()}
}

func diskTestDocuments(docs []DiskDocument) iter.Seq2[DiskDocument, error] {
	return func(yield func(DiskDocument, error) bool) {
		for _, doc := range docs {
			if !yield(doc, nil) {
				return
			}
		}
	}
}
