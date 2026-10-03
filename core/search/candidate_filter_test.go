package search

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCandidateFilterSharesCorpusStatistics(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	index := NewInvertedIndex[string, Document](fakeAnalyzer{}, BM25{K1: DefaultBM25K1, B: DefaultBM25B}, compareStringID, WithPositions(), WithIndexClock(func() time.Time { return now }))
	visible := func(id string) bool { return strings.HasPrefix(id, "visible:") }
	ctx := WithCandidateFilter(context.Background(), visible)
	for id, text := range map[string]string{"visible:a": "project launch", "visible:b": "project project long document", "hidden:best": "project project project project launch"} {
		if err := index.Index(id, Text(text)); err != nil {
			t.Fatal(err)
		}
	}
	check := func() []Result[string] {
		t.Helper()
		var last []Result[string]
		for _, test := range []struct {
			query string
			opts  MatchOptions
		}{{"project", MatchOptions{}}, {"project launch", MatchOptions{Mode: MatchAll}}, {"pro", MatchOptions{PrefixTerms: true}}, {"projec", MatchOptions{Fuzziness: 1}}} {
			for _, k := range []int{1, 10} {
				got, _, err := index.SearchMatchTopKCandidatesContextAt(ctx, test.query, k, nil, test.opts, Budget{}, now, nil)
				want := exhaustiveTopK(index.SearchMatch(test.query, test.opts), k, visible)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("query=%q k=%d got=%v want=%v error=%v", test.query, k, got, want, err)
				}
				if test.query == "project" && k == 10 {
					last = got
				}
			}
		}
		got, _, err := index.SearchPhraseTopKCandidatesContextAt(ctx, "project launch", 1, nil, Budget{}, now, nil)
		want := exhaustiveTopK(index.SearchPhrase("project launch"), 1, visible)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("phrase=%v want=%v error=%v", got, want, err)
		}
		return last
	}
	before := check()
	for i := range 20 {
		if err := index.Index(fmt.Sprintf("hidden:%d", i), Text("ordinary unrelated long private document")); err != nil {
			t.Fatal(err)
		}
	}
	after := check()
	if reflect.DeepEqual(before, after) {
		t.Fatal("private data did not participate in the shared ranking corpus")
	}
	if err := index.Index("hidden:best", Text("unrelated")); err != nil {
		t.Fatal(err)
	}
	check()
	index.Delete("hidden:0")
	check()
	if err := index.IndexWithExpiration("hidden:ttl", Text("project launch"), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	check()
	now = now.Add(2 * time.Minute)
	check()
	if _, exists := index.ords.lookup("hidden:ttl"); exists {
		t.Fatal("shared TTL cleanup did not remove expired postings")
	}
	index.Compact()
	want := check()
	items := []PreparedItem[string]{}
	for _, entry := range index.docs {
		// Rebuild a fresh logical corpus from the indexed text held by this
		// fixture; no candidate bitmap or permission statistics are persisted.
		text := "ordinary unrelated long private document"
		switch entry.id {
		case "visible:a":
			text = "project launch"
		case "visible:b":
			text = "project project long document"
		case "hidden:best":
			text = "unrelated"
		}
		prepared, _, err := index.Prepare(Text(text))
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, PreparedItem[string]{ID: entry.id, Prepared: prepared})
	}
	if err := index.RebuildPrepared(items); err != nil {
		t.Fatal(err)
	}
	if got := check(); !reflect.DeepEqual(got, want) {
		t.Fatal("restored shared statistics differ", got, want)
	}
	separate := NewInvertedIndex[string, Document](fakeAnalyzer{}, BM25{K1: DefaultBM25K1, B: DefaultBM25B}, compareStringID)
	if err := separate.Index("other:corpus", Text(strings.Repeat("project ", 100))); err != nil {
		t.Fatal(err)
	}
	if got := check(); !reflect.DeepEqual(got, want) {
		t.Fatal("independent corpus affected this index", got, want)
	}
	beforeMemory := index.MemoryStats()
	for i := range 1000 {
		prefix := fmt.Sprintf("scope:%d:", i)
		queryCtx := WithCandidateFilter(context.Background(), func(id string) bool { return strings.HasPrefix(id, prefix) })
		if _, _, err := index.SearchMatchTopKCandidatesContextAt(queryCtx, "project", 1, nil, MatchOptions{}, Budget{}, now, nil); err != nil {
			t.Fatal(err)
		}
	}
	if after := index.MemoryStats(); after.EstimatedRetainedBytes != beforeMemory.EstimatedRetainedBytes || after.Generation != beforeMemory.Generation {
		t.Fatal("distinct filters retained statistics or rewrote index", beforeMemory, after)
	}

}
