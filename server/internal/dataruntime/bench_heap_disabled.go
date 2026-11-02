//go:build !lantern_bench_heap || (!linux && !darwin)

package dataruntime

// Normal builds do not import the measurement package or install counters.
func benchHeapApply(_ *Executor) func() { return func() {} }
