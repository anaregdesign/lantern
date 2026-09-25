package service

import (
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func mustMutationLogEntry(t *testing.T, log *mutationlog.Log, seq uint64) mutationlog.Entry {
	t.Helper()
	entries, cancel, err := log.Subscribe(seq)
	if err != nil {
		t.Fatalf("Subscribe(%d): %v", seq, err)
	}
	defer func() { _ = cancel() }()
	select {
	case entry := <-entries:
		if entry.Seq != seq {
			t.Fatalf("log entry seq = %d, want %d", entry.Seq, seq)
		}
		return entry
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out reading log entry %d", seq)
		return mutationlog.Entry{}
	}
}

func mustGraphMutation(t *testing.T, op mutationlog.MutationOp) *pb.Mutation {
	t.Helper()
	mutation, ok := graphMutationFromLog(op)
	if !ok {
		t.Fatalf("graph mutation unavailable from %T", op)
	}
	return mutation
}
