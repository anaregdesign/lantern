package graphcache

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
)

func wholeStateTestCache() *GraphCache[string, string] {
	cache := NewGraphCacheWithStaging[string, string](time.Hour)
	cache.EnablePrefixIndex(func(key string) string { return key })
	cache.EnableSearchIndex(
		func(key, value string) search.Document { return search.Text(key + " " + value) },
		strings.Compare,
	)
	return cache
}

func TestWholeStateInstallCommitAbortAndSearchRebuild(t *testing.T) {
	live := wholeStateTestCache()
	if err := live.PutVertex("old", "old document"); err != nil {
		t.Fatal(err)
	}
	candidate := wholeStateTestCache()
	stamp := hlc.Timestamp{WallNs: time.Now().UnixNano(), NodeID: hlc.NodeID{1}}
	if !candidate.PutVertexWithExpirationHLC("new", "searchable candidate", time.Now().Add(time.Hour), stamp) {
		t.Fatal("candidate vertex rejected")
	}
	if !candidate.PutEdgeWithExpirationHLC("new", "head", 2, time.Now().Add(time.Hour), stamp) {
		t.Fatal("candidate edge rejected")
	}
	if err := candidate.CompleteSearchIndexRecovery(); err != nil {
		t.Fatal(err)
	}

	stage, err := live.BeginWholeStateInstall(candidate)
	if err != nil {
		t.Fatal(err)
	}
	stage.Abort()
	if value, ok := live.GetVertex("old"); !ok || value != "old document" {
		t.Fatalf("abort lost live vertex: %q, %t", value, ok)
	}
	if _, ok := live.GetVertex("new"); ok {
		t.Fatal("abort retained candidate vertex")
	}

	stage, err = live.BeginWholeStateInstall(candidate)
	if err != nil {
		t.Fatal(err)
	}
	stage.Commit()
	if _, ok := live.GetVertex("old"); ok {
		t.Fatal("commit retained replaced vertex")
	}
	if value, ok := live.GetVertex("new"); !ok || value != "searchable candidate" {
		t.Fatalf("commit lost candidate vertex: %q, %t", value, ok)
	}
	if weight, _, ok := live.GetEdgeDetail("new", "head"); !ok || weight != 2 {
		t.Fatalf("commit lost candidate edge: %v, %t", weight, ok)
	}
	if got := live.CountByPrefix("n"); got != 1 {
		t.Fatalf("prefix index = %d, want 1", got)
	}
	hits := live.SearchVertices("searchable", 10, "")
	if len(hits) == 0 || hits[0].ID != "new" {
		t.Fatalf("search index = %+v", hits)
	}
	if snapshot := live.SnapshotReplication(); len(snapshot.Graph.Vertices) != 2 {
		t.Fatalf("installed graph snapshot = %+v", snapshot.Graph.Vertices)
	}

	t.Run("preserves receiver observability counters", func(t *testing.T) {
		live := NewGraphCacheWithStaging[string, string](time.Hour)
		candidate := NewGraphCacheWithStaging[string, string](time.Hour)
		limits := CausalMetadataLimits{MaxVertexEntries: 1, MaxEdgeEntries: 1}
		live.SetCausalMetadataLimits(limits)
		candidate.SetCausalMetadataLimits(limits)
		expiration := time.Now().Add(time.Hour)
		if _, err := live.PutVerticesWithExpirationHLCOutcomesChecked(
			[]VertexItem[string, string]{{Key: "old", Value: "old", Expiration: expiration}},
			stamp,
		); err != nil {
			t.Fatal(err)
		}
		newer := stamp
		newer.WallNs++
		if _, err := live.PutVerticesWithExpirationHLCOutcomesChecked(
			[]VertexItem[string, string]{{Key: "rejected", Value: "rejected", Expiration: expiration}},
			newer,
		); err == nil {
			t.Fatal("receiver fixture did not reject causal capacity")
		} else {
			var capacity *CausalMetadataCapacityError
			if !errors.As(err, &capacity) {
				t.Fatalf("receiver capacity error = %v", err)
			}
		}
		if _, err := candidate.PutVerticesWithExpirationHLCOutcomesChecked(
			[]VertexItem[string, string]{{Key: "new", Value: "new", Expiration: expiration}},
			stamp,
		); err != nil {
			t.Fatal(err)
		}
		before := live.CausalMetadataStats()
		stage, err := live.BeginWholeStateInstall(candidate)
		if err != nil {
			t.Fatal(err)
		}
		stage.Commit()
		after := live.CausalMetadataStats()
		if after.VertexRejected != before.VertexRejected ||
			after.VertexEntriesHighWater < before.VertexEntriesHighWater ||
			after.VertexEstimatedBytesHighWater < before.VertexEstimatedBytesHighWater {
			t.Fatalf("whole-state install regressed receiver metrics: before=%+v after=%+v", before, after)
		}
	})
}
