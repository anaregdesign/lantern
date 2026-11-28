package search

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/tarp"
)

func TestAdvanceDiskBasePublishesOneCompleteCut(t *testing.T) {
	ctx := t.Context()
	source := diskTestSource()
	deadline := source.Watermark.Add(time.Second)
	docs := []DiskDocument{
		{Key: "a", Document: Text("engine soleword"), Version: []byte{1}},
		{Key: "b", Document: Text(strings.Repeat("engine ", 1050) + "lantern"), Version: []byte{2}},
		{Key: "c", Document: Text("expiry engine"), Expiration: deadline},
	}
	for i := range 270 {
		docs = append(docs, DiskDocument{Key: fmt.Sprintf("shared/%04d", i), Document: Text("engine lantern")})
	}
	docs = append(docs, DiskDocument{Key: "unrelated", Document: Text("unrelated")})
	changes := []DiskDocument{
		{Key: "a"},
		{Key: "b", Document: Text("newword engine"), Version: []byte{3}},
		{Key: "shared/0253", Document: Text("restart engine"), Version: []byte{4}},
		{Key: "z", Document: Text(strings.Repeat("fresh ", 1050) + "engine"), Expiration: deadline.Add(time.Hour), Version: []byte{5}},
	}
	dir := t.TempDir()
	db, err := tarp.Open(dir, tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	analyzer := NewScriptAwareAnalyzer()
	if err := BuildDiskBase(ctx, db, 1, source, diskTestDocuments(docs), analyzer, WithPositions()); err != nil {
		t.Fatal(err)
	}
	old, err := OpenDiskIndex(ctx, db, 1, source, analyzer, nil, WithPositions())
	if err != nil {
		t.Fatal(err)
	}
	next := source
	next.Sequence++
	next.Digest[0] = 1
	next.Watermark = deadline
	cases := []struct {
		query  string
		opts   MatchOptions
		phrase bool
	}{
		{"engine lantern", MatchOptions{}, false},
		{"engine", MatchOptions{}, false},
		{"soleword expiry", MatchOptions{}, false},
		{"fresh engine", MatchOptions{}, true},
		{"eng", MatchOptions{PrefixTerms: true}, false},
		{"restarts", MatchOptions{Fuzziness: 1}, false},
	}
	want := make([][]Result[string], len(cases))
	for i, tc := range cases {
		want[i], _, err = old.SearchAtWithChanges(ctx, tc.query, 6, nil, tc.opts, tc.phrase, Budget{}, deadline, diskTestDocuments(changes))
		if err != nil {
			t.Fatal(err)
		}
	}
	beforeCommits := db.Stats().WriteCommits
	if _, err = old.AdvanceDiskBase(ctx, next, diskTestDocuments(changes), Budget{MaxChangeVisits: 1}); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("bounded advancement returned %v", err)
	}
	if db.Stats().WriteCommits != beforeCommits {
		t.Fatal("rejected advancement committed rows")
	}
	prior, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := old.AdvanceDiskBase(ctx, next, diskTestDocuments(changes), Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if !sameDiskSource(sealed.Source(), next) || db.Stats().WriteCommits != beforeCommits+1 {
		t.Fatal("successful advancement must publish one matching commit")
	}
	if sealed.footer.Records+2 != db.Stats().LiveKeys {
		t.Fatalf("footer rows %d, live Tarp rows %d", sealed.footer.Records, db.Stats().LiveKeys)
	}
	oldVersion, err := prior.Get(diskKey(1, diskFooterRow))
	if err != nil || !bytes.Equal(oldVersion, old.seal) {
		t.Fatalf("old snapshot lost prior footer: %v", err)
	}
	if _, err := prior.Get(diskKey(1, diskDocIDRow, uint64(1))); err != nil {
		t.Fatalf("pinned old view lost removed Vertex: %v", err)
	}
	if err := prior.Close(); err != nil {
		t.Fatal(err)
	}
	key := diskKey(1, diskDocIDRow, uint64(1))
	if _, err := db.Get(key); !errors.Is(err, tarp.ErrNotFound) {
		t.Fatalf("removed Vertex remained visible: %v", err)
	}
	if _, _, err := old.Search(ctx, "engine", 4, nil, MatchOptions{}, false, Budget{}); !errors.Is(err, ErrDiskIndex) {
		t.Fatalf("superseded handle returned %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = tarp.Open(dir, tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sealed, err = OpenDiskIndex(ctx, db, 1, next, analyzer, nil, WithPositions())
	if err != nil {
		t.Fatal(err)
	}
	for i, tc := range cases {
		got, stats, err := sealed.SearchAt(ctx, tc.query, 6, nil, tc.opts, tc.phrase, Budget{}, deadline)
		if err != nil || len(got) != len(want[i]) || stats.ChangeVisits != 0 || stats.ExpirationVisits != 0 {
			t.Fatalf("%q reopened materialization = %v, %v; want %v", tc.query, got, err, want[i])
		}
		for j := range got {
			if got[j].ID != want[i][j].ID || math.Float64bits(got[j].Score) != math.Float64bits(want[i][j].Score) {
				t.Fatalf("%q reopened materialization = %v; want %v", tc.query, got, want[i])
			}
		}
	}
	beforeCommits = db.Stats().WriteCommits
	unchanged, err := sealed.AdvanceDiskBase(ctx, next, diskTestDocuments(nil), Budget{})
	if err != nil || unchanged != sealed || db.Stats().WriteCommits != beforeCommits {
		t.Fatalf("unchanged source emitted a disk commit: %v", err)
	}
	reapedSource := next
	reapedSource.Watermark = deadline.Add(time.Hour)
	reaped, err := sealed.AdvanceDiskBase(ctx, reapedSource, nil, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if !reaped.NextDeadline().IsZero() || reaped.footer.Records+2 != db.Stats().LiveKeys {
		t.Fatal("TTL-only materialization must erase only the due document and its rows")
	}
	got, stats, err := reaped.Search(ctx, "fresh engine", 6, nil, MatchOptions{}, true, Budget{})
	if err != nil || len(got) != 0 || stats.ExpirationVisits != 0 {
		t.Fatalf("TTL-only materialized results = %v, statistics = %+v, error = %v", got, stats, err)
	}

}
