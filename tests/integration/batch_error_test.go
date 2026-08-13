package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	client "github.com/anaregdesign/lantern/sdks/go"
)

// TestBatchError_PartialWrite verifies that when a later chunk fails, the
// SDK returns a *BatchError whose Written field reflects fully-committed
// prior chunks, and that the wrapped error still satisfies errors.Is for the
// underlying sentinel.
func TestBatchError_PartialWrite(t *testing.T) {
	l, cleanup := newInProcessClientChunked(t, 2)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	exp := time.Now().Add(time.Minute)
	inputs := []client.VertexInput{
		{Key: "ok-1", Value: "v", Expiration: exp},
		{Key: "ok-2", Value: "v", Expiration: exp},
		{Key: "", Value: "v", Expiration: exp}, // empty key trips the server-side validator
		{Key: "ok-4", Value: "v", Expiration: exp},
	}
	_, err := l.PutVertices(ctx, inputs)
	if err == nil {
		t.Fatal("PutVertices: expected BatchError, got nil")
	}

	var be *client.BatchError
	if !errors.As(err, &be) {
		t.Fatalf("PutVertices: expected *BatchError, got %T: %v", err, err)
	}
	if be.Written != 2 {
		t.Errorf("BatchError.Written = %d, want 2 (first chunk committed)", be.Written)
	}
	if !errors.Is(err, client.ErrInvalidArgument) {
		t.Errorf("expected errors.Is(err, ErrInvalidArgument) to be true; err = %v", err)
	}

	// Confirm the first chunk did commit.
	v, gerr := l.GetVertex(ctx, "ok-1")
	if gerr != nil || v == nil {
		t.Errorf("ok-1 should exist after partial write; v=%v err=%v", v, gerr)
	}
}
