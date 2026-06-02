package graph

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestPutEdgeWithExpiration_Atomic asserts that a reader observing an edge
// that is being continually replaced via PutEdgeWithExpiration NEVER sees a
// transient "missing" state. The previous service-level implementation
// performed DeleteEdge + AddEdgeWithExpiration as two separate cache calls,
// which exposed a window where concurrent GetEdge readers observed a
// spurious NotFound. Run with -race to also catch any introduced races.
func TestPutEdgeWithExpiration_Atomic(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	exp := time.Now().Add(time.Minute)
	// Seed an edge so the first reader iterations are guaranteed to see it.
	c.AddEdgeWithExpiration("T", "H", 1, exp)

	const writers = 4
	const readers = 16
	const writesPerWriter = 2000

	var wg sync.WaitGroup
	var missing int64
	var reads int64
	stop := make(chan struct{})

	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _, ok := c.GetEdgeDetail("T", "H")
				if !ok {
					atomic.AddInt64(&missing, 1)
				}
				atomic.AddInt64(&reads, 1)
			}
		}()
	}

	var writerWG sync.WaitGroup
	for w := 0; w < writers; w++ {
		writerWG.Add(1)
		go func(w int) {
			defer writerWG.Done()
			for i := 0; i < writesPerWriter; i++ {
				c.PutEdgeWithExpiration("T", "H", float32(w*1000+i+1), exp)
			}
		}(w)
	}
	writerWG.Wait()
	close(stop)
	wg.Wait()

	if missing != 0 {
		t.Fatalf("PutEdgeWithExpiration leaked NotFound to readers: %d of %d reads observed a missing edge",
			missing, reads)
	}
}
