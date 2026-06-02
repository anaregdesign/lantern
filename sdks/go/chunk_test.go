package client

import "testing"

func TestChunkSliceFallsBackToDefaultWhenSizeNonPositive(t *testing.T) {
	// 2*defaultBatchChunkSize elements must produce 2 chunks of defaultBatchChunkSize
	// when size <= 0 (regression: previously returned a single oversized chunk
	// that could trip the server's MaxBatchSize validator).
	n := defaultBatchChunkSize * 2
	in := make([]int, n)
	for _, size := range []int{0, -1, -100} {
		chunks := chunkSlice(in, size)
		if len(chunks) != 2 {
			t.Errorf("size=%d: len(chunks) = %d, want 2", size, len(chunks))
			continue
		}
		for i, c := range chunks {
			if len(c) != defaultBatchChunkSize {
				t.Errorf("size=%d chunk[%d] len = %d, want %d", size, i, len(c), defaultBatchChunkSize)
			}
		}
	}
}

func TestChunkSlicePositiveSize(t *testing.T) {
	in := make([]int, 10)
	chunks := chunkSlice(in, 3)
	if len(chunks) != 4 {
		t.Fatalf("len(chunks) = %d, want 4", len(chunks))
	}
	if len(chunks[3]) != 1 {
		t.Errorf("last chunk len = %d, want 1", len(chunks[3]))
	}
}
