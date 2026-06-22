package search

import "testing"

func TestTermDict(t *testing.T) {
	d := newTermDict()

	t.Run("intern assigns stable, distinct ids", func(t *testing.T) {
		a1 := d.intern("alpha")
		b := d.intern("beta")
		a2 := d.intern("alpha")
		if a1 != a2 {
			t.Fatalf("intern not stable: alpha got %d then %d", a1, a2)
		}
		if a1 == b {
			t.Fatalf("distinct terms share id %d", a1)
		}
		if got := d.len(); got != 2 {
			t.Fatalf("len = %d, want 2", got)
		}
	})

	t.Run("lookup hits interned and misses unknown without growing", func(t *testing.T) {
		id, ok := d.lookup("alpha")
		if !ok {
			t.Fatal("lookup(alpha) missed an interned term")
		}
		if id != d.intern("alpha") {
			t.Fatalf("lookup id %d disagrees with intern", id)
		}
		if _, ok := d.lookup("missing"); ok {
			t.Fatal("lookup(missing) reported a hit")
		}
		if d.len() != 2 {
			t.Fatalf("lookup must not assign an id; len grew to %d", d.len())
		}
	})

	t.Run("release frees the id and hands it back out", func(t *testing.T) {
		alpha, _ := d.lookup("alpha")
		d.release(alpha)
		if _, ok := d.lookup("alpha"); ok {
			t.Fatal("released term still looks up")
		}
		if d.len() != 1 {
			t.Fatalf("len after release = %d, want 1", d.len())
		}
		// The next fresh term reuses the freed id rather than growing the space.
		if got := d.intern("gamma"); got != alpha {
			t.Fatalf("intern after release = %d, want reused %d", got, alpha)
		}
		if d.len() != 2 {
			t.Fatalf("len after reuse = %d, want 2", d.len())
		}
	})
}
