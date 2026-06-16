package graphcache

import (
	"sort"
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
