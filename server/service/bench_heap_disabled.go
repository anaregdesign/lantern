//go:build !lantern_bench_heap || (!linux && !darwin)

package service

// Normal builds do not import the measurement package or install counters.
func benchHeapApply(_ *LanternService) func() { return func() {} }
