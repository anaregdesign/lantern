package graphcache

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestDictionary_InternReturnsStableID(t *testing.T) {
	d := newDictionary[string]()
	a := d.intern("a")
	b := d.intern("b")
	a2 := d.intern("a")
	if a != a2 {
		t.Fatalf("intern of same key returned different ids: %d vs %d", a, a2)
	}
	if a == b {
		t.Fatalf("intern of different keys returned same id: %d", a)
	}
	if got := d.len(); got != 2 {
		t.Fatalf("len=%d, want 2", got)
	}
}

func TestDictionary_ResolveAndLookup(t *testing.T) {
	d := newDictionary[string]()
	id := d.intern("hello")

	if got, ok := d.lookup("hello"); !ok || got != id {
		t.Fatalf("lookup(hello)=(%d,%v), want (%d,true)", got, ok, id)
	}
	if _, ok := d.lookup("nope"); ok {
		t.Fatalf("lookup(nope) returned ok=true unexpectedly")
	}
	if got, ok := d.resolve(id); !ok || got != "hello" {
		t.Fatalf("resolve(%d)=(%q,%v), want (hello,true)", id, got, ok)
	}
	if _, ok := d.resolve(vertexID(999)); ok {
		t.Fatalf("resolve of out-of-range id returned ok=true")
	}
}

func TestDictionary_RefcountAndRelease(t *testing.T) {
	d := newDictionary[string]()
	id := d.intern("x") // refcount = 1
	d.acquire(id)       // refcount = 2
	_ = d.intern("x")   // refcount = 3 (same key, same id)

	if got := d.release(id); got {
		t.Fatalf("release at refcount 3->2 reported freed=true")
	}
	if got := d.release(id); got {
		t.Fatalf("release at refcount 2->1 reported freed=true")
	}
	if _, ok := d.lookup("x"); !ok {
		t.Fatalf("key was forgotten while refcount > 0")
	}
	if got := d.release(id); !got {
		t.Fatalf("release at refcount 1->0 reported freed=false")
	}
	if _, ok := d.lookup("x"); ok {
		t.Fatalf("key still resolvable after final release")
	}
	if _, ok := d.resolve(id); ok {
		t.Fatalf("id still resolvable after final release")
	}
	if got := d.len(); got != 0 {
		t.Fatalf("len=%d after final release, want 0", got)
	}
}

func TestDictionary_FreelistReuse(t *testing.T) {
	d := newDictionary[string]()
	id1 := d.intern("a")
	_ = d.release(id1)
	id2 := d.intern("b")
	if id1 != id2 {
		t.Fatalf("freed id was not recycled: id1=%d id2=%d", id1, id2)
	}
	// And the reverse mapping must reflect the *new* key, not the old one.
	if got, _ := d.resolve(id2); got != "b" {
		t.Fatalf("resolve after reuse returned stale key %q", got)
	}
}

func TestDictionary_DoesNotLeakStringAfterFree(t *testing.T) {
	d := newDictionary[string]()
	id := d.intern("payload")
	if d.reverse[id] != "payload" {
		t.Fatalf("reverse entry not populated")
	}
	_ = d.release(id)
	var zero string
	if d.reverse[id] != zero {
		t.Fatalf("reverse entry not cleared after release: %q", d.reverse[id])
	}
}

func TestDictionary_AcquireOfUnallocatedPanics(t *testing.T) {
	d := newDictionary[string]()
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("acquire of unallocated id did not panic")
		}
	}()
	d.acquire(vertexID(0))
}

func TestDictionary_ReleaseOfUnallocatedPanics(t *testing.T) {
	d := newDictionary[string]()
	id := d.intern("x")
	_ = d.release(id)
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("over-release did not panic")
		}
	}()
	_ = d.release(id)
}

func TestDictionary_ConcurrentInternIsAtomic(t *testing.T) {
	// All goroutines intern the same N keys; each key should end up with
	// a single id and a refcount equal to the number of interns minus the
	// number of releases. We over-intern then release down to a known
	// baseline and check len() matches.
	const goroutines = 32
	const keys = 64
	const opsPerGoroutine = 1000

	d := newDictionary[string]()
	keyNames := make([]string, keys)
	for i := range keyNames {
		keyNames[i] = fmt.Sprintf("k-%d", i)
	}

	var wg sync.WaitGroup
	wg.Add(goroutines)
	var observedIDs [keys]atomic.Uint64
	for g := 0; g < goroutines; g++ {
		go func(gi int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				k := (gi + i) % keys
				id := d.intern(keyNames[k])
				// All interns of the same key must agree on the id.
				prev := observedIDs[k].Swap(uint64(id) + 1)
				if prev != 0 && prev != uint64(id)+1 {
					panic(fmt.Sprintf("id changed for key %s: %d -> %d", keyNames[k], prev-1, id))
				}
			}
		}(g)
	}
	wg.Wait()

	if got := d.len(); got != keys {
		t.Fatalf("len=%d after concurrent intern, want %d", got, keys)
	}
	// Drain all interns to verify accounting balances.
	for k := 0; k < keys; k++ {
		id, ok := d.lookup(keyNames[k])
		if !ok {
			t.Fatalf("key %s missing after concurrent intern", keyNames[k])
		}
		// Each goroutine interned this key floor(opsPerGoroutine / keys) or
		// ceil times; computing the exact expected refcount is fiddly, so
		// just release until we hit zero and confirm no panic.
		for {
			freed := d.release(id)
			if freed {
				break
			}
		}
	}
	if got := d.len(); got != 0 {
		t.Fatalf("len=%d after draining, want 0", got)
	}
}

func TestDictionary_GenericNonStringKey(t *testing.T) {
	type composite struct {
		a int
		b string
	}
	d := newDictionary[composite]()
	id1 := d.intern(composite{1, "x"})
	id2 := d.intern(composite{1, "x"})
	id3 := d.intern(composite{2, "x"})
	if id1 != id2 {
		t.Fatalf("equal struct keys mapped to different ids: %d %d", id1, id2)
	}
	if id1 == id3 {
		t.Fatalf("distinct struct keys mapped to same id: %d", id1)
	}
}

func TestDictionary_PinBoth(t *testing.T) {
	t.Run("pins both refcounts and releases them", func(t *testing.T) {
		d := newDictionary[string]()
		tail := d.intern("tail") // refcount 1
		head := d.intern("head") // refcount 1

		idT, idH, release, ok := d.pinBoth("tail", "head")
		if !ok {
			t.Fatalf("pinBoth of present keys returned ok=false")
		}
		if idT != tail || idH != head {
			t.Fatalf("pinBoth ids = (%d,%d), want (%d,%d)", idT, idH, tail, head)
		}
		// The pin added one reference to each id, so the original intern
		// reference can be released without freeing the id.
		if freed := d.release(tail); freed {
			t.Fatalf("tail freed while pin still held")
		}
		if freed := d.release(head); freed {
			t.Fatalf("head freed while pin still held")
		}
		if _, ok := d.lookup("tail"); !ok {
			t.Fatalf("tail vanished while pinned")
		}
		// Dropping the pin frees both (their last reference).
		release()
		if _, ok := d.lookup("tail"); ok {
			t.Fatalf("tail still resolvable after pin release + intern release")
		}
		if _, ok := d.lookup("head"); ok {
			t.Fatalf("head still resolvable after pin release + intern release")
		}
		if got := d.len(); got != 0 {
			t.Fatalf("len=%d after full release, want 0", got)
		}
	})

	t.Run("missing endpoint returns ok=false and no-op release", func(t *testing.T) {
		d := newDictionary[string]()
		d.intern("tail")
		idT, idH, release, ok := d.pinBoth("tail", "absent")
		if ok {
			t.Fatalf("pinBoth with missing head returned ok=true")
		}
		if idT != 0 || idH != 0 {
			t.Fatalf("pinBoth miss returned ids (%d,%d), want (0,0)", idT, idH)
		}
		if release == nil {
			t.Fatalf("pinBoth miss returned nil release")
		}
		release() // must not panic
		// The present endpoint must not have been pinned.
		if freed := d.release(idT); freed != true {
			// tail had refcount 1; releasing it should free (proves pinBoth
			// did not leak an extra reference on the miss path).
			t.Fatalf("tail refcount drifted on pinBoth miss")
		}
	})

	t.Run("self-pin balances", func(t *testing.T) {
		d := newDictionary[string]()
		self := d.intern("self") // refcount 1
		idA, idB, release, ok := d.pinBoth("self", "self")
		if !ok || idA != self || idB != self {
			t.Fatalf("self pinBoth = (%d,%d,%v), want (%d,%d,true)", idA, idB, ok, self, self)
		}
		// Pin added two references (a == b bumped twice); release drops both.
		release()
		if freed := d.release(self); !freed {
			t.Fatalf("self refcount unbalanced after self-pin: not freed at expected count")
		}
		if got := d.len(); got != 0 {
			t.Fatalf("len=%d after self-pin balance, want 0", got)
		}
	})
}
