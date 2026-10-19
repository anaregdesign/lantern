//go:build lantern_test_time

package main

import (
	"log/slog"
	"os"
	"testing"

	"github.com/anaregdesign/lantern/server/decisiontrace"
	"github.com/anaregdesign/lantern/server/internal/listenerlaunch"
)

// A private serving executable made with go test -c. It runs the real App,
// listener adoption, certification and cleanup; only UTC composition is the
// compile-time controlled fixture. Normal binaries cannot select this entry or
// mint fixture time. go test itself supplies -test.* arguments and runs tests.
func TestMain(m *testing.M) {
	if len(os.Args) == 1 || os.Args[1] == listenerlaunch.Argument {
		if err := runMain(); err != nil {
			decisiontrace.WriteStartupFailure(os.Stderr, err)
			slog.Error("functional server exited with error", slog.Any("err", err))
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
