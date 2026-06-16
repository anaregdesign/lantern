package graphcache

import (
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/search"
)

// textExtract is the value projection used by the string-keyed tests: it makes
// the stored string value itself the searchable document.
func textExtract(s string) search.Document { return search.Text(s) }

// keys returns just the IDs of a ranked result set, in rank order, for terse
// assertions.
func keys(results []search.Result[string]) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.ID
	}
	return out
}

func TestGraphCache_SearchDisabledByDefault(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.PutVertex("k", "lantern graph database")
	// Without EnableSearchIndex the search path is inert: no index work on
	// put, and SearchVertices returns nil.
	if got := c.SearchVertices("database", 10, ""); got != nil {
		t.Fatalf("SearchVertices without enable: got %v want nil", got)
	}
}

func TestGraphCache_EnableSearchIndex_AfterPutPanics(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.PutVertex("k", "v")
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when enabling search index on non-empty cache")
		}
	}()
	c.EnableSearchIndex(textExtract)
}

func TestGraphCache_EnableSearchIndex_NilExtractPanics(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when extract is nil")
		}
	}()
	c.EnableSearchIndex(nil)
}

func TestGraphCache_EnableSearchIndex_Idempotent(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnableSearchIndex(textExtract)
	c.EnableSearchIndex(textExtract) // must not panic
}

func TestGraphCache_SearchVertices(t *testing.T) {
	t.Run("Ranking", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract)
		c.PutVertex("exact", "arch")
		c.PutVertex("infix", "research laboratory")
		c.PutVertex("other", "a giant panda")

		got := c.SearchVertices("arch", 10, "")
		// "panda" never shares a gram with "arch"; the exact short doc must
		// outrank the longer infix match (BM25 length normalization).
		if want := []string{"exact", "infix"}; !equalKeys(keys(got), want) {
			t.Fatalf("SearchVertices(arch) = %v, want %v", keys(got), want)
		}
	})

	t.Run("Overwrite drops stale postings", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract)
		c.PutVertex("k", "alpha")
		if got := keys(c.SearchVertices("alpha", 10, "")); !equalKeys(got, []string{"k"}) {
			t.Fatalf("before overwrite: SearchVertices(alpha) = %v, want [k]", got)
		}
		// Re-index on overwrite is the key difference from the prefix index:
		// the old value's postings must disappear, the new value's appear.
		c.PutVertex("k", "omega zulu")
		if got := c.SearchVertices("alpha", 10, ""); got != nil {
			t.Fatalf("after overwrite: SearchVertices(alpha) = %v, want nil", keys(got))
		}
		if got := keys(c.SearchVertices("omega", 10, "")); !equalKeys(got, []string{"k"}) {
			t.Fatalf("after overwrite: SearchVertices(omega) = %v, want [k]", got)
		}
	})

	t.Run("Delete stops matching", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract)
		c.PutVertex("k", "gamma unique")
		if got := keys(c.SearchVertices("gamma", 10, "")); !equalKeys(got, []string{"k"}) {
			t.Fatalf("before delete: got %v, want [k]", got)
		}
		// DeleteVertex routes through the vertices.SetOnEvict hook, which must
		// physically drop the posting from the search index.
		if !c.DeleteVertex("k") {
			t.Fatal("DeleteVertex(k) reported not-present")
		}
		if got := c.SearchVertices("gamma", 10, ""); got != nil {
			t.Fatalf("after delete: got %v, want nil", keys(got))
		}
	})

	t.Run("Clear stops matching", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract)
		c.PutVertex("a", "shared term")
		c.PutVertex("b", "shared word")
		// The inner cache's Clear fires SetOnEvict for every key, so the
		// search index must end up empty too.
		c.vertices.Clear()
		if got := c.SearchVertices("shared", 10, ""); got != nil {
			t.Fatalf("after clear: got %v, want nil", keys(got))
		}
	})

	t.Run("TTL expiry stops matching", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract)
		// Live and already-expired vertices share the term; only the live one
		// must surface, even before the GC Flush runs (liveness filter).
		c.PutVertex("live", "delta payload")
		c.PutVertexWithExpiration("dead", "delta payload", time.Now().Add(-time.Minute))
		if got := keys(c.SearchVertices("delta", 10, "")); !equalKeys(got, []string{"live"}) {
			t.Fatalf("before flush: got %v, want [live]", got)
		}
		// After the Flush physically evicts the expired vertex, the index drops
		// its posting via SetOnEvict and the result is unchanged.
		c.vertices.Flush()
		if got := keys(c.SearchVertices("delta", 10, "")); !equalKeys(got, []string{"live"}) {
			t.Fatalf("after flush: got %v, want [live]", got)
		}
	})

	t.Run("KeyPrefix scopes results", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract)
		c.PutVertex("user:1", "common keyword")
		c.PutVertex("user:2", "common keyword")
		c.PutVertex("session:a", "common keyword")
		got := c.SearchVertices("keyword", 10, "user:")
		if want := []string{"user:1", "user:2"}; !sameSet(keys(got), want) {
			t.Fatalf("SearchVertices(keyword, prefix user:) = %v, want %v", keys(got), want)
		}
	})

	t.Run("Limit caps results", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract)
		for _, k := range []string{"k1", "k2", "k3", "k4"} {
			c.PutVertex(k, "common keyword")
		}
		if got := c.SearchVertices("keyword", 2, ""); len(got) != 2 {
			t.Fatalf("SearchVertices(keyword, limit 2) returned %d hits, want 2", len(got))
		}
	})

	t.Run("Empty query returns nil", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract)
		c.PutVertex("k", "anything")
		if got := c.SearchVertices("", 10, ""); got != nil {
			t.Fatalf("SearchVertices(empty) = %v, want nil", keys(got))
		}
	})

	t.Run("Non-positive limit returns nil", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract)
		c.PutVertex("k", "anything")
		if got := c.SearchVertices("anything", 0, ""); got != nil {
			t.Fatalf("SearchVertices(limit 0) = %v, want nil", keys(got))
		}
		if got := c.SearchVertices("anything", -1, ""); got != nil {
			t.Fatalf("SearchVertices(limit -1) = %v, want nil", keys(got))
		}
	})

	t.Run("No match returns nil", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract)
		c.PutVertex("k", "alpha beta")
		if got := c.SearchVertices("zzzzz", 10, ""); got != nil {
			t.Fatalf("SearchVertices(no-match) = %v, want nil", keys(got))
		}
	})
}

// equalKeys reports whether got equals want in the same order.
func equalKeys(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// sameSet reports whether got and want contain the same keys regardless of
// order — used where rank order between equally-scored docs is unspecified.
func sameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]int, len(got))
	for _, k := range got {
		seen[k]++
	}
	for _, k := range want {
		if seen[k] == 0 {
			return false
		}
		seen[k]--
	}
	return true
}
