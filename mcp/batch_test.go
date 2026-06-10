package mcp

import (
	"errors"
	"fmt"
	"testing"

	client "github.com/anaregdesign/lantern/sdks/go"
)

func TestBatchWritten_NilMeansAllCommitted(t *testing.T) {
	written, cause := batchWritten(5, nil)
	if written != 5 || cause != nil {
		t.Fatalf("batchWritten(5, nil) = (%d, %v), want (5, nil)", written, cause)
	}
}

func TestBatchWritten_BatchErrorYieldsWrittenAndCause(t *testing.T) {
	boom := errors.New("boom")
	written, cause := batchWritten(5, &client.BatchError{Written: 3, Err: boom})
	if written != 3 {
		t.Fatalf("written = %d, want 3", written)
	}
	if !errors.Is(cause, boom) {
		t.Fatalf("cause = %v, want to wrap boom", cause)
	}
}

func TestBatchWritten_WrappedBatchErrorIsUnwrapped(t *testing.T) {
	boom := errors.New("boom")
	wrapped := fmt.Errorf("context: %w", &client.BatchError{Written: 2, Err: boom})
	written, cause := batchWritten(5, wrapped)
	if written != 2 {
		t.Fatalf("written = %d, want 2 (errors.As must see through the wrap)", written)
	}
	if !errors.Is(cause, boom) {
		t.Fatalf("cause = %v, want to wrap boom", cause)
	}
}

func TestBatchWritten_PlainErrorMeansNothingCommitted(t *testing.T) {
	boom := errors.New("pre-flight failure")
	written, cause := batchWritten(5, boom)
	if written != 0 {
		t.Fatalf("written = %d, want 0 for a non-BatchError", written)
	}
	if !errors.Is(cause, boom) {
		t.Fatalf("cause = %v, want boom", cause)
	}
}
