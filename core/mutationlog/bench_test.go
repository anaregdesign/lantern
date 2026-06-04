package mutationlog

import "testing"

// BenchmarkAppendAtCapacity exercises the eviction path that issue #252
// turned from O(N) (shift-copy of the entire ring) into O(1) head bumping.
// The pre-fix implementation degrades sharply as Capacity grows; the
// post-fix implementation is flat.
func BenchmarkAppendAtCapacity(b *testing.B) {
	const capacity = 100_000
	l := New(Options{Capacity: capacity, SubscriberBuffer: 1})
	// Pre-fill so every iteration below hits the eviction branch.
	for i := 0; i < capacity; i++ {
		if _, err := l.Append(i, ts(int64(i))); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := l.Append(i, ts(int64(i))); err != nil {
			b.Fatal(err)
		}
	}
}
