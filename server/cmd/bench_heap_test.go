//go:build lantern_bench_heap && lantern_test_time && (linux || darwin)

package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

// This opt-in exists only in the explicit dual-tag controlled App executable.
// Neither the normal binary nor a normal-time measurement image reads it.
func init() {
	if directory := os.Getenv("LANTERN_TEST_BENCH_HEAP_DIR"); directory != "" {
		benchHeapDirectory = directory
	}
}

func TestBenchHeapRefusalSurvivesCancellation(t *testing.T) {
	for _, failPublish := range []bool{false, true} {
		t.Run(map[bool]string{false: "refusal", true: "publish_failure"}[failPublish], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			notifications := make(chan error, 1)
			var refusal benchHeapRefusal
			publishFailure := errors.New("refusal publication failed")
			published := 0
			joined := make(chan struct{})
			go func() {
				defer close(joined)
				refusal.record("request", func() error {
					published++
					if failPublish {
						return publishFailure
					}
					return nil
				}, notifications, cancel)
				refusal.record("later", func() error { published++; return nil }, notifications, cancel)
			}()
			// Deliberately take cancellation without receiving the notification,
			// matching App's legal cancellation-versus-error select ordering.
			<-ctx.Done()
			<-joined
			shutdownFailure := errors.Join(refusal.err, nil, nil)
			if shutdownFailure == nil || !strings.Contains(shutdownFailure.Error(), "request") || strings.Contains(shutdownFailure.Error(), "later") || published != 1 {
				t.Fatal("first refusal lost at shutdown", shutdownFailure, published)
			}
			if errors.Is(shutdownFailure, publishFailure) != failPublish {
				t.Fatal("publication failure lost", shutdownFailure)
			}
			if notified := <-notifications; notified != refusal.err {
				t.Fatal("notification differs from retained refusal")
			}
		})
	}
}
