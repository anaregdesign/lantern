package slice

import (
	"context"
	"github.com/anaregdesign/lantern/pkg/core/model/function"
	"golang.org/x/sync/semaphore"
	"runtime"
	"sync"
)

func ForEach[T any](ctx context.Context, slice []T, consumer function.Consumer[T]) {
	var wg sync.WaitGroup
	sem := semaphore.NewWeighted(int64(runtime.NumCPU()))
	for _, element := range slice {
		wg.Add(1)
		sem.Acquire(ctx, 1)
		go func(e T) {
			defer sem.Release(1)
			defer wg.Done()
			consumer(e)
		}(element)
	}
	wg.Wait()
}

func Map[S any, T any](ctx context.Context, slice []S, function function.Function[S, T]) []T {
	var wg sync.WaitGroup
	sem := semaphore.NewWeighted(int64(runtime.NumCPU()))
	result := make([]T, len(slice))
	for i, element := range slice {
		wg.Add(1)
		sem.Acquire(ctx, 1)
		go func(i int, e S) {
			defer sem.Release(1)
			defer wg.Done()
			result[i] = function(e)
		}(i, element)
	}
	wg.Wait()
	return result
}

func Reduce[T any](ctx context.Context, slice []T, operator function.Operator[T]) T {
	result := slice[0]
	for _, element := range slice[1:] {
		result = operator(result, element)
	}
	return result
}

func Filter[T any](ctx context.Context, slice []T, predicate function.Predicate[T]) []T {
	result := make([]T, 0)
	for _, element := range slice {
		if predicate(element) {
			result = append(result, element)
		}
	}
	return result
}

func Contains[T comparable](slice []T, element T) bool {
	for _, e := range slice {
		if e == element {
			return true
		}
	}
	return false
}
