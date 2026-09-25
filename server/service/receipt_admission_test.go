package service

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

func waitForExclusiveReceiptAdmission(t *testing.T, admission *receiptOperationAdmission) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		admission.mu.Lock()
		exclusive := admission.exclusive
		admission.mu.Unlock()
		if exclusive {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("exclusive receipt admission was not claimed")
		}
		runtime.Gosched()
	}
}

func TestReceiptOperationAdmissionSharesAndPrioritizesExclusiveOwner(t *testing.T) {
	admission := newReceiptOperationAdmission()
	releaseFirst, ok := admission.tryAcquireShared()
	if !ok {
		t.Fatal("first shared operation was rejected")
	}
	releaseSecond, ok := admission.tryAcquireShared()
	if !ok {
		t.Fatal("concurrent shared operation was rejected")
	}
	releaseSecond()

	exclusiveReady := make(chan func(), 1)
	go func() {
		release, err := admission.acquireExclusive(context.Background())
		if err != nil {
			exclusiveReady <- nil
			return
		}
		exclusiveReady <- release
	}()
	waitForExclusiveReceiptAdmission(t, admission)
	if release, ok := admission.tryAcquireShared(); ok {
		release()
		t.Fatal("shared operation crossed a claimed exclusive install")
	}

	releaseFirst()
	releaseExclusive := waitReceiptTest(t, "exclusive receipt admission", exclusiveReady)
	if releaseExclusive == nil {
		t.Fatal("exclusive receipt admission failed")
	}
	if release, ok := admission.tryAcquireShared(); ok {
		release()
		releaseExclusive()
		t.Fatal("shared operation crossed an active exclusive install")
	}
	releaseExclusive()
	if release, ok := admission.tryAcquireShared(); !ok {
		t.Fatal("shared operation starved after exclusive install")
	} else {
		release()
	}
}

func TestReceiptOperationAdmissionExclusiveWaitIsContextAware(t *testing.T) {
	admission := newReceiptOperationAdmission()
	releaseShared, ok := admission.tryAcquireShared()
	if !ok {
		t.Fatal("shared operation was rejected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	exclusiveDone := make(chan error, 1)
	go func() {
		_, err := admission.acquireExclusive(ctx)
		exclusiveDone <- err
	}()
	waitForExclusiveReceiptAdmission(t, admission)
	cancel()
	if err := waitReceiptTest(t, "canceled exclusive admission", exclusiveDone); !errors.Is(err, context.Canceled) {
		t.Fatalf("exclusive admission error = %v, want context.Canceled", err)
	}
	if release, ok := admission.tryAcquireShared(); !ok {
		t.Fatal("canceled exclusive admission left readers blocked")
	} else {
		release()
	}
	releaseShared()
}
