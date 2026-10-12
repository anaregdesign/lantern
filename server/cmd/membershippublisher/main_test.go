package main

import (
	"io"
	"testing"
)

func TestRequiresExplicitPrivateInputsAndMode(t *testing.T) {
	for _, args := range [][]string{nil, {"-unknown"}, {"-mode", "fresh"}, {"-mode", "resume", "extra"}} {
		if run(t.Context(), args, io.Discard) == nil {
			t.Fatal("unqualified publisher started")
		}
	}
}
