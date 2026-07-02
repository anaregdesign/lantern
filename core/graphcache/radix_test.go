package graphcache

import (
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// collectAll returns every key stored in r, in the lexicographic order
// walkPrefix yields. Used as the equality oracle for the property tests.
func collectAll(r *radix) []string {
	var out []string
	r.walkPrefix("", func(k string) bool {
		out = append(out, k)
		return true
	})
	return out
}

func TestRadix_InsertAndContains(t *testing.T) {
	r := newRadix()
	keys := []string{"a", "ab", "abc", "abd", "b", "bar", "baz", ""}
	for _, k := range keys {
		if !r.insert(k) {
			t.Fatalf("insert(%q): expected new=true on first insert", k)
		}
	}
	for _, k := range keys {
		if r.insert(k) {
			t.Fatalf("insert(%q): expected new=false on second insert", k)
		}
	}
	if got, want := r.len(), len(keys); got != want {
		t.Fatalf("len: got %d want %d", got, want)
	}
}

func TestRadix_WalkPrefixLexOrder(t *testing.T) {
	r := newRadix()
	in := []string{"banana", "band", "bandana", "ape", "apex", "apricot", "ap"}
	for _, k := range in {
		r.insert(k)
	}

	cases := []struct {
		prefix string
		want   []string
	}{
		{"", append([]string{}, in...)},
		{"a", []string{"ap", "ape", "apex", "apricot"}},
		{"ap", []string{"ap", "ape", "apex", "apricot"}},
		{"ape", []string{"ape", "apex"}},
		{"b", []string{"banana", "band", "bandana"}},
		{"band", []string{"band", "bandana"}},
		{"bandana", []string{"bandana"}},
		{"bandanaX", nil},
		{"z", nil},
	}
	for _, tc := range cases {
		t.Run(tc.prefix, func(t *testing.T) {
			var got []string
			r.walkPrefix(tc.prefix, func(k string) bool {
				got = append(got, k)
				return true
			})
			want := append([]string{}, tc.want...)
			sort.Strings(want)
			if !equalSlices(got, want) {
				t.Fatalf("walkPrefix(%q):\n  got  %v\n  want %v", tc.prefix, got, want)
			}
		})
	}
}

func TestRadix_WalkPrefixEarlyExit(t *testing.T) {
	r := newRadix()
	for _, k := range []string{"a", "ab", "abc", "abcd"} {
		r.insert(k)
	}
	var seen []string
	r.walkPrefix("a", func(k string) bool {
		seen = append(seen, k)
		return len(seen) < 2 // stop after the second visit
	})
	if len(seen) != 2 {
		t.Fatalf("expected early exit at 2 visits, got %d (%v)", len(seen), seen)
	}
}

func TestRadix_CountPrefix(t *testing.T) {
	r := newRadix()
	for _, k := range []string{"foo", "foobar", "foobaz", "fool", "bar", ""} {
		r.insert(k)
	}
	cases := map[string]int{
		"":        6,
		"f":       4,
		"foo":     4,
		"foob":    2,
		"foobar":  1,
		"foobazz": 0,
		"x":       0,
	}
	for prefix, want := range cases {
		if got := r.countPrefix(prefix); got != want {
			t.Errorf("countPrefix(%q): got %d want %d", prefix, got, want)
		}
	}
}

func TestRadix_DeleteAndCompaction(t *testing.T) {
	r := newRadix()
	keys := []string{"alpha", "alphabet", "alpine", "alps", "beta"}
	for _, k := range keys {
		r.insert(k)
	}
	// Deleting an interior key should not affect the surviving paths.
	if !r.delete("alpha") {
		t.Fatal("delete(alpha) returned false on a present key")
	}
	if r.delete("alpha") {
		t.Fatal("delete(alpha) returned true on an absent key (second call)")
	}
	want := []string{"alphabet", "alpine", "alps", "beta"}
	if got := collectAll(r); !equalSlices(got, want) {
		t.Fatalf("after delete:\n  got  %v\n  want %v", got, want)
	}
	if got := r.len(); got != 4 {
		t.Fatalf("len after delete: got %d want 4", got)
	}

	// Repeated insert/delete cycles should leave the tree shaped the
	// same as a fresh build (lex order preserved, no orphaned nodes).
	for _, k := range []string{"alphabet", "alpine"} {
		r.delete(k)
	}
	for _, k := range []string{"alphabet", "alpine"} {
		r.insert(k)
	}
	if got := collectAll(r); !equalSlices(got, want) {
		t.Fatalf("after churn:\n  got  %v\n  want %v", got, want)
	}
}

// TestRadix_DeleteMany verifies the batch delete removes every present key,
// skips absent keys, decrements size only for keys actually removed, returns
// that count, and is a no-op for an empty input (#738).
func TestRadix_DeleteMany(t *testing.T) {
	r := newRadix()
	keys := []string{"alpha", "alphabet", "alpine", "alps", "beta"}
	for _, k := range keys {
		r.insert(k)
	}

	if got := r.deleteMany(nil); got != 0 {
		t.Fatalf("deleteMany(nil) = %d, want 0", got)
	}

	// "ghost" is absent and must not be counted; "alpha" appears twice and
	// must be counted once (the second pass is an absent-key no-op).
	n := r.deleteMany([]string{"alpha", "alpine", "ghost", "alpha"})
	if n != 2 {
		t.Fatalf("deleteMany removed = %d, want 2", n)
	}
	want := []string{"alphabet", "alps", "beta"}
	if got := collectAll(r); !equalSlices(got, want) {
		t.Fatalf("after deleteMany:\n  got  %v\n  want %v", got, want)
	}
	if got := r.len(); got != 3 {
		t.Fatalf("len after deleteMany: got %d want 3", got)
	}
}

func TestRadix_EmptyKey(t *testing.T) {
	r := newRadix()
	if !r.insert("") {
		t.Fatal("insert(\"\") returned false on first call")
	}
	if r.countPrefix("") != 1 {
		t.Fatal("countPrefix(\"\") expected 1")
	}
	got := collectAll(r)
	if !equalSlices(got, []string{""}) {
		t.Fatalf("collectAll: got %v", got)
	}
	if !r.delete("") {
		t.Fatal("delete(\"\") returned false")
	}
	if r.len() != 0 {
		t.Fatalf("len after delete(\"\"): got %d want 0", r.len())
	}
}

func TestRadix_SplitCorrectness(t *testing.T) {
	// Triggers the edge-split branch: insert "abcdef", then "abcXYZ"
	// which forces splitting the existing edge at "abc".
	r := newRadix()
	r.insert("abcdef")
	r.insert("abcXYZ")
	r.insert("abc")
	got := collectAll(r)
	want := []string{"abc", "abcXYZ", "abcdef"}
	if !equalSlices(got, want) {
		t.Fatalf("after split:\n  got  %v\n  want %v", got, want)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRadixWalkPrefixBound property-checks the seek walker (#836) against a
// naive filter over every stored key, across randomized key sets and
// boundary shapes: bounds landing mid-edge-label, exactly on stored keys,
// below the prefix subtree, past its end, and the empty bound.
func TestRadixWalkPrefixBound(t *testing.T) {
	rng := rand.New(rand.NewSource(836))
	alphabet := []string{"a", "b", "ab", "ba", "session:", "msg:", ":", "0", "1"}
	randKey := func() string {
		n := 1 + rng.Intn(4)
		var sb strings.Builder
		for i := 0; i < n; i++ {
			sb.WriteString(alphabet[rng.Intn(len(alphabet))])
		}
		return sb.String()
	}

	for trial := 0; trial < 200; trial++ {
		r := newRadix()
		keys := map[string]bool{}
		for i := 0; i < 30; i++ {
			k := randKey()
			keys[k] = true
			r.insert(k)
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)

		prefixes := []string{"", "a", "ab", "session:", "zzz", sorted[rng.Intn(len(sorted))]}
		bounds := []string{"", "a", sorted[rng.Intn(len(sorted))], sorted[rng.Intn(len(sorted))] + "x", "zzzz", "\x00"}
		for _, prefix := range prefixes {
			for _, bound := range bounds {
				for _, inclusive := range []bool{false, true} {
					var want []string
					for _, k := range sorted {
						if !strings.HasPrefix(k, prefix) {
							continue
						}
						if k > bound || (inclusive && k == bound) {
							want = append(want, k)
						}
					}
					var got []string
					r.walkPrefixBound(prefix, bound, inclusive, func(k string) bool {
						got = append(got, k)
						return true
					})
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("trial %d prefix=%q bound=%q incl=%v:\n got  %v\n want %v\n keys %v",
							trial, prefix, bound, inclusive, got, want, sorted)
					}
				}
			}
		}

		// Early stop must truncate, not skip.
		if len(sorted) > 2 {
			var got []string
			r.walkPrefixBound("", sorted[0], false, func(k string) bool {
				got = append(got, k)
				return len(got) < 2
			})
			if len(got) != 2 || got[0] != sorted[1] {
				t.Fatalf("early stop got %v, want first two after %q of %v", got, sorted[0], sorted)
			}
		}
	}
}
