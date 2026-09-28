package main

import (
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect"
)

func TestTransientPublication(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"read overlap", connect.NewError(connect.CodeUnavailable, errors.New("graph publication changed during read; retry")), true},
		{"snapshot gap", connect.NewError(connect.CodeFailedPrecondition, errors.New("gapped: mutation publication or Snapshot install requires repair before reading, subscribing, or taking a snapshot")), true},
		{"other unavailable", connect.NewError(connect.CodeUnavailable, errors.New("peer down")), false},
		{"other failed precondition", connect.NewError(connect.CodeFailedPrecondition, errors.New("invalid topology")), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := transientPublication(fmt.Errorf("verify BFS: %w", tc.err)); got != tc.want {
				t.Fatalf("transientPublication = %t, want %t", got, tc.want)
			}
		})
	}
}
