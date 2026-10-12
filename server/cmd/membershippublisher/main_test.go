package main

import (
	"io"
	"testing"
)

func TestRequiresExplicitPrivateInputsAndMode(t *testing.T) {
	for _, args := range [][]string{nil, {"-unknown"}, {"-mode", "fresh"}, {"-mode", "resume", "extra"},
		{"-mode", "advance-window"}, {"-mode", "resume", "-next-config", "/must-not-open"},
		{"-maintenance-warning", "0"}, {"-maintenance-warning", "-1h"}} {
		if run(t.Context(), args, io.Discard) == nil {
			t.Fatal("unqualified publisher started")
		}
	}
}
