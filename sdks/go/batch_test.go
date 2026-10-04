package client

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

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

func TestRunBatchWriteBlindAcceptanceSendsEachNewChunkOnce(t *testing.T) {
	for _, failLast := range []bool{false, true} {
		var calls [][]int
		l := &Lantern{opts: options{batchChunkSize: 2}}
		count, err := runBatchWrite(context.Background(), l, []int{0, 1, 2, 3, 4}, func(_ context.Context, chunk []int) (int32, error) {
			calls = append(calls, append([]int(nil), chunk...))
			if chunk[0] == 2 {
				return 0, &MutationAcceptance{}
			}
			if chunk[0] == 4 && failLast {
				return 0, ErrUnavailable
			}
			return int32(len(chunk)), nil
		})
		if !reflect.DeepEqual(calls, [][]int{{0, 1}, {2, 3}, {4}}) || count != 0 {
			t.Fatal("chunk resend or effect count leaked", calls, count)
		}
		if failLast {
			var batch *BatchError
			if !errors.As(err, &batch) || batch.Written != 4 || !errors.Is(err, ErrUnavailable) {
				t.Fatal("partial failure lost", err)
			}
		} else if _, accepted := err.(*MutationAcceptance); !accepted {
			t.Fatal("complete call did not acknowledge undisclosed effects", err)
		}
	}
}
