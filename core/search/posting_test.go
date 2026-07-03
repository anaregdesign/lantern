package search

import "testing"

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
		q.set(1, 2, []uint32{3, 7}) // two occurrences at positions 3 and 7
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
}

func TestPostingListClampsTF(t *testing.T) {
	p := newPostingList()
	p.set(1, 1<<20, nil) // far above the uint16 ceiling
	if got, want := p.tf(1), int(^uint16(0)); got != want {
		t.Fatalf("tf clamp = %d, want %d", got, want)
	}
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
