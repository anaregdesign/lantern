//go:build lantern_bench_heap && !linux && !darwin

package main

import (
	"context"
	"errors"
)

func startBenchHeapObservation(context.Context, *App) (func() error, <-chan error, error) {
	return nil, nil, errors.New("unsupported heap observation platform")
}
