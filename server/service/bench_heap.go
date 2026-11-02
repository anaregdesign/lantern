//go:build lantern_bench_heap && (linux || darwin)

package service

import (
	"errors"

	"github.com/anaregdesign/lantern/server/internal/dataruntime"
)

func (s *LanternServer) ObserveBenchHeap(o *dataruntime.BenchHeapObserver) error {
	if s == nil || s.server == nil || s.server.Handler == nil || o == nil {
		return errors.New("missing public heap observation handler")
	}
	s.server.Handler = o.WrapPublic(s.server.Handler)
	return nil
}

// BenchHeapObserver is available only in the existing measurement build.
type BenchHeapObserver = dataruntime.BenchHeapObserver

func NewBenchHeapObserver(s *LanternService) (*BenchHeapObserver, error) {
	return dataruntime.NewBenchHeapObserver(s)
}
