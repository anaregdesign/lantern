package search

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/tarp"
)

func TestDiskExpiryUpdatesStatsAndVocabularyAtQueryCut(t *testing.T) {
	ctx := t.Context()
	source := diskTestSource()
	deadline := source.Watermark.Add(time.Second)
	docs := []DiskDocument{
		{Key: "expired1", Document: Fields{{FieldKey, "lantern"}, {FieldValue, "lantern lantern storage soleexpiry unique"}}, Version: []byte{1}, Expiration: deadline},
		{Key: "expired2", Document: Fields{{FieldKey, "lantern"}, {FieldValue, "engine engine lantern"}}, Version: []byte{2}, Expiration: deadline},
		{Key: "live1", Document: Fields{{FieldKey, "lantern"}, {FieldValue, "lantern storage"}}},
		{Key: "live2", Document: Fields{{FieldKey, "lantern"}, {FieldValue, "lantern portable storage"}}},
		{Key: "live3", Document: Fields{{FieldValue, "soleexpansion"}}},
	}
	analyzer := NewScriptAwareAnalyzer()
	ram := NewInvertedIndex[string, Document](analyzer, nil, strings.Compare, WithPositions(), WithIndexClock(func() time.Time { return source.Watermark }))
	for _, doc := range docs {
		if err := ram.IndexWithExpiration(doc.Key, doc.Document, doc.Expiration); err != nil {
			t.Fatal(err)
		}
	}
	db, err := tarp.Open(t.TempDir(), tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := BuildDiskBase(ctx, db, 1, source, diskTestDocuments(docs), analyzer, WithPositions()); err != nil {
		t.Fatal(err)
	}
	disk, err := OpenDiskIndex(ctx, db, 1, source, analyzer, nil, WithPositions())
	if err != nil {
		t.Fatal(err)
	}
	for _, now := range []time.Time{deadline.Add(-time.Nanosecond), deadline, deadline.Add(time.Second)} {
		for _, test := range []struct {
			query string
			opts  MatchOptions
		}{
			{"lantern storage", MatchOptions{}},
			{"soleexpiry", MatchOptions{}},
			{"soleexp", MatchOptions{PrefixTerms: true}},
			{"soleexpansio", MatchOptions{Fuzziness: 1}},
		} {
			want, _, err := ram.SearchMatchTopKContextAt(ctx, test.query, 10, nil, test.opts, Budget{}, now)
			if err != nil {
				t.Fatal(err)
			}
			got, stats, err := disk.SearchAt(ctx, test.query, 10, nil, test.opts, false, Budget{}, now)
			if err != nil || len(got) != len(want) || (!now.Before(deadline) && stats.ExpirationVisits != 2) {
				t.Fatalf("query %q at %s: disk = %v, %v, RAM = %v", test.query, now, got, err, want)
			}
			for i := range want {
				if got[i].ID != want[i].ID || math.Float64bits(got[i].Score) != math.Float64bits(want[i].Score) {
					t.Fatalf("query %q at %s: disk = %v, RAM = %v", test.query, now, got, want)
				}
			}
		}
	}
	got, _, err := disk.SearchAt(ctx, "lantern", 10, nil, MatchOptions{}, false, Budget{MaxExpirationVisits: 1}, deadline)
	if got != nil || !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("expiry cap returned %v, %v", got, err)
	}
	key := diskKey(1, diskForwardRow, uint64(1), uint8(ClassWord), "lantern")
	if err := db.Delete(key); err != nil {
		t.Fatal(err)
	}
	got, _, err = disk.SearchAt(ctx, "lantern", 10, nil, MatchOptions{}, false, Budget{}, deadline)
	if got != nil || !errors.Is(err, ErrDiskIndex) {
		t.Fatalf("missing forward reference returned %v, %v", got, err)
	}
}
