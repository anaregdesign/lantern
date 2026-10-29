//go:build !lantern_bench_heap

package main

import "context"

// A normal executable installs no measurement wrapper, signal or file control.
func startBenchHeapObservation(context.Context, *App) (func() error, <-chan error, error) {
	return nil, nil, nil
}
