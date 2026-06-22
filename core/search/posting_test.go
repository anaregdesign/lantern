package search

import "testing"

func TestPostingList(t *testing.T) {
	p := newPostingList()

	t.Run("set records membership; tf defaults to 1, overrides above it", func(t *testing.T) {
		p.set(10, 1)
		p.set(20, 3) // tf > 1 is recorded in tfHi
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
}

func TestPostingListClampsTF(t *testing.T) {
	p := newPostingList()
	p.set(1, 1<<20) // far above the uint16 ceiling
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
