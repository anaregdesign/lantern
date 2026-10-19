package streamcancel

import (
	"context"
	"testing"
	"time"
)

func TestInterruptWithdrawsOnlyOwnedRequest(t *testing.T) {
	parent := context.Background()
	request, cancel := context.WithTimeout(parent, time.Minute)
	defer cancel()
	bound, _ := request.Deadline()
	ctx := WithInterrupt(request, cancel)
	if got, ok := ctx.Deadline(); !ok || !got.Equal(bound) {
		t.Fatal("interruption changed the existing deadline")
	}
	if !Interrupt(ctx) || !Interrupt(ctx) {
		t.Fatal("owned interruption capability missing")
	}
	if request.Err() != context.Canceled || parent.Err() != nil {
		t.Fatal("interruption did not stay within the owned request")
	}
	if Interrupt(parent) {
		t.Fatal("unowned context acquired an interruption capability")
	}
}
