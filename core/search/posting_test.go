package search

import (
	"math/rand"
	"slices"
	"testing"
)

func TestPostingList(t *testing.T) {
	p := newPostingList()

	t.Run("set records membership; tf defaults to 1, overrides above it", func(t *testing.T) {
		p.set(10, 1, nil)
		p.set(20, 3, nil) // tf > 1 is recorded in tfHi
		if got := p.cardinality(); got != 2 {
			t.Fatalf("cardinality = %d, want 2", got)
		}
		if got := p.tf(10); got != 1 {
			t.Fatalf("tf(10) = %d, want 1 (default, no override stored)", got)
		}
		if got := p.tf(20); got != 3 {
			t.Fatalf("tf(20) = %d, want 3 (override)", got)
		}
	})

	t.Run("remove drops membership, clears override, reports empty on last", func(t *testing.T) {
		if empty := p.remove(20); empty {
			t.Fatal("remove(20) reported empty while 10 is still present")
		}
		if got := p.tf(20); got != 1 {
			t.Fatalf("tf(20) after remove = %d, want 1 (override cleared)", got)
		}
		if empty := p.remove(10); !empty {
			t.Fatal("remove(10) did not report empty after the last document left")
		}
		if got := p.cardinality(); got != 0 {
			t.Fatalf("cardinality after emptying = %d, want 0", got)
		}
	})

	t.Run("positions are recorded, read back, and dropped on remove", func(t *testing.T) {
		q := newPostingList()
		q.set(1, 2, []uint64{3, 7}) // two occurrences at positions 3 and 7
		q.set(2, 1, nil)            // present, but no positions tracked
		if got := q.positionsOf(1); len(got) != 2 || got[0] != 3 || got[1] != 7 {
			t.Fatalf("positionsOf(1) = %v, want [3 7]", got)
		}
		if got := q.positionsOf(2); got != nil {
			t.Fatalf("positionsOf(2) = %v, want nil (set with nil positions)", got)
		}
		if empty := q.remove(1); empty {
			t.Fatal("remove(1) reported empty while ord 2 remains")
		}
		if got := q.positionsOf(1); got != nil {
			t.Fatalf("positionsOf(1) after remove = %v, want nil (positions dropped)", got)
		}
	})

	t.Run("default fast path migrates when structured fields appear", func(t *testing.T) {
		q := newPostingList()
		q.set(1, 1, nil)
		var fields [numDocumentFields]preparedFieldTerm
		fields[FieldKey] = preparedFieldTerm{frequency: 1}
		q.setFields(2, fields)
		if !q.containsField(1, FieldDefault) || q.containsField(2, FieldDefault) || !q.containsField(2, FieldKey) {
			t.Fatalf("field membership default/key = %t/%t/%t", q.containsField(1, FieldDefault), q.containsField(2, FieldDefault), q.containsField(2, FieldKey))
		}
		if q.fieldCardinality(FieldDefault) != 1 || q.fieldCardinality(FieldKey) != 1 {
			t.Fatalf("field cardinalities = %d/%d", q.fieldCardinality(FieldDefault), q.fieldCardinality(FieldKey))
		}
	})
}

func TestPostingListClampsTF(t *testing.T) {
	p := newPostingList()
	p.set(1, 1<<20, nil) // far above the uint16 ceiling
	if got, want := p.tf(1), int(^uint16(0)); got != want {
		t.Fatalf("tf clamp = %d, want %d", got, want)
	}
}

// TestPackPositions proves the delta+varint position encoding (#908) is
// lossless: unpack(pack(x)) reproduces the original ascending positions for
// edge cases and for randomized sequences with wide, multi-byte-varint gaps.
// This is the equivalence reference for the phrase/proximity readers, which now
// consume the packed store via positionsOf.
func TestPackPositions(t *testing.T) {
	t.Run("edge cases round-trip; empty packs to no bytes", func(t *testing.T) {
		cases := [][]uint64{
			nil,
			{0},
			{5},
			{0, 1, 2, 3},
			{3, 7, 100, 128, 300},
			{0, 1 << 7, 1 << 14, 1 << 21, 1 << 28}, // each gap crosses a varint width boundary
			{1<<16 + 1, 1<<20 + 5},                 // beyond the uint16 fast-path ceiling
		}
		for _, in := range cases {
			if got := unpackPositions(packPositions(in)); !slices.Equal(got, in) {
				t.Fatalf("round-trip %v = %v", in, got)
			}
		}
		if packed := packPositions(nil); len(packed) != 0 {
			t.Fatalf("packPositions(nil) = %v, want no bytes", packed)
		}
	})

	t.Run("randomized ascending sequences round-trip", func(t *testing.T) {
		rng := rand.New(rand.NewSource(1))
		for iter := 0; iter < 2000; iter++ {
			seq := make([]uint64, rng.Intn(64))
			var acc uint64
			for i := range seq {
				acc += uint64(rng.Intn(1<<20) + 1) // strictly ascending, wide gaps
				seq[i] = acc
			}
			if got := unpackPositions(packPositions(seq)); !slices.Equal(got, seq) {
				t.Fatalf("iter %d round-trip mismatch:\n in=%v\nout=%v", iter, seq, got)
			}
		}
	})

	t.Run("decode reuses caller scratch", func(t *testing.T) {
		dst := make([]uint64, 0, 8)
		got := unpackPositionsInto(dst, packPositions([]uint64{2, 5, 9}))
		if !slices.Equal(got, []uint64{2, 5, 9}) {
			t.Fatalf("decoded = %v", got)
		}
		if &got[0] != &dst[:1][0] {
			t.Fatal("decode replaced sufficient caller scratch")
		}
	})
}
func TestOrdinals(t *testing.T) {
	o := newOrdinals[string]()

	a := o.assign("a")
	b := o.assign("b")
	if a == b {
		t.Fatalf("distinct keys share ordinal %d", a)
	}
	if got := o.assign("a"); got != a {
		t.Fatalf("assign not stable: a = %d then %d", a, got)
	}
	if id, ok := o.lookup("a"); !ok || id != a {
		t.Fatalf("lookup(a) = %d,%v want %d,true", id, ok, a)
	}
	if _, ok := o.lookup("missing"); ok {
		t.Fatal("lookup(missing) reported a hit")
	}

	o.release("a", a)
	if _, ok := o.lookup("a"); ok {
		t.Fatal("released key still looks up")
	}
	// The next fresh key reuses the freed ordinal rather than growing the space.
	if got := o.assign("c"); got != a {
		t.Fatalf("assign after release = %d, want reused %d", got, a)
	}
}
