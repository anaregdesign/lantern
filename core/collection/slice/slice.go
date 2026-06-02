package slice

import (
	"context"
	"runtime"
	"slices"
	"sync"

	"github.com/anaregdesign/lantern/core/model/function"
	"golang.org/x/sync/semaphore"
)

// ForEach applies consumer to each element of slice, fanning out up to NumCPU
// goroutines. It returns when every element has been processed or ctx is
// cancelled; in the cancelled case in-flight goroutines are still awaited.
func ForEach[T any](ctx context.Context, slice []T, consumer function.Consumer[T]) {
	var wg sync.WaitGroup
	sem := semaphore.NewWeighted(int64(runtime.NumCPU()))
	for _, e := range slice {
		if err := sem.Acquire(ctx, 1); err != nil {
			break
		}
		wg.Add(1)
		go func() {
			defer sem.Release(1)
			defer wg.Done()
			consumer(e)
		}()
	}
	wg.Wait()
}

// Map applies fn to each element of slice in parallel and returns the results
// in input order. If ctx is cancelled, untouched slots are left zero-valued.
func Map[S any, T any](ctx context.Context, slice []S, fn function.Function[S, T]) []T {
	var wg sync.WaitGroup
	sem := semaphore.NewWeighted(int64(runtime.NumCPU()))
	result := make([]T, len(slice))
	for i, e := range slice {
		if err := sem.Acquire(ctx, 1); err != nil {
			break
		}
		wg.Add(1)
		go func() {
			defer sem.Release(1)
			defer wg.Done()
			result[i] = fn(e)
		}()
	}
	wg.Wait()
	return result
}

// Reduce folds slice left-to-right using operator. Panics on an empty slice;
// callers should guard accordingly.
func Reduce[T any](slice []T, operator function.Operator[T]) T {
	result := slice[0]
	for _, e := range slice[1:] {
		result = operator(result, e)
	}
	return result
}

// Filter returns a new slice containing only the elements that satisfy
// predicate. The result is preallocated to the input length to avoid repeated
// growth in the common case where most elements pass.
func Filter[T any](slice []T, predicate function.Predicate[T]) []T {
	result := make([]T, 0, len(slice))
	for _, e := range slice {
		if predicate(e) {
			result = append(result, e)
		}
	}
	return result
}

// Contains reports whether element is present in slice. Thin wrapper around the
// stdlib slices.Contains so callers can keep using this package consistently.
func Contains[T comparable](slice []T, element T) bool {
	return slices.Contains(slice, element)
}
