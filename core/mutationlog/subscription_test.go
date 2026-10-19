package mutationlog

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

func TestSubscriptionSignalsOverflowWithoutDrainingDelivery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dropped := make(chan string, 1)
		log := New(Options{Capacity: 10000, SubscriberBuffer: 512, OnDrop: func(cause string) { dropped <- cause }})
		defer log.Close()
		stream, err := log.SubscribeStream(1, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		if _, err := log.Append(1, ts(1)); err != nil {
			t.Fatal(err)
		}
		// Seq1 is held at delivery; no sleep or wall-clock scheduling assumption.
		synctest.Wait()
		for i := 2; i <= 514; i++ {
			if _, err := log.Append(i, ts(int64(i))); err != nil {
				t.Fatal(err)
			}
		}
		synctest.Wait()
		select {
		case cause := <-dropped:
			if cause != DropCauseBufferFull {
				t.Fatalf("drop cause = %q", cause)
			}
		default:
			t.Fatal("fixture did not overflow")
		}
		// No entries have been consumed, but loss must already be observable.
		select {
		case <-stream.Done():
		default:
			t.Fatal("terminal loss waited behind buffered delivery")
		}
		if !errors.Is(stream.Err(), ErrSubscriptionLost) {
			t.Fatalf("terminal error = %v, want subscription loss", stream.Err())
		}
		if first, ok := log.FirstSeq(); !ok || first != 1 {
			t.Fatalf("history not retained at drop: first=%d exists=%t", first, ok)
		}
		if err := stream.Close(); err != nil {
			t.Fatal(err)
		}
		if _, ok := <-stream.Entries(); ok {
			t.Fatal("Close returned before delivery stopped")
		}
		// A retained tail remains replayable; overflow does not demand Snapshot.
		resumed, err := log.SubscribeStream(1, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer resumed.Close()
		for i := uint64(1); i <= 514; i++ {
			entry, ok := <-resumed.Entries()
			if !ok || entry.Seq != i {
				t.Fatalf("retained replay[%d] = %+v, open=%t, error=%v", i, entry, ok, resumed.Err())
			}
		}
	})
}

func TestSubscriptionCancelJoinsBlockedDelivery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log := New(Options{})
		defer log.Close()
		stream, err := log.SubscribeStream(1, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := log.Append(1, ts(1)); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		for range 2 {
			if err := stream.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if !errors.Is(stream.Err(), context.Canceled) {
			t.Fatalf("cancel error = %v", stream.Err())
		}
		if _, ok := <-stream.Entries(); ok {
			t.Fatal("delivery survived Close")
		}
	})
}

func TestSubscriptionReplayEvictionReportsMissingHistory(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log := New(Options{Capacity: 2})
		defer log.Close()
		for i := 1; i <= 2; i++ {
			if _, err := log.Append(i, ts(int64(i))); err != nil {
				t.Fatal(err)
			}
		}
		stream, err := log.SubscribeStream(1, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		synctest.Wait()
		for i := 3; i <= 5; i++ {
			if _, err := log.Append(i, ts(int64(i))); err != nil {
				t.Fatal(err)
			}
		}
		for i := uint64(1); i <= 2; i++ {
			if entry := <-stream.Entries(); entry.Seq != i {
				t.Fatalf("copied replay[%d] = %+v", i, entry)
			}
		}
		if _, ok := <-stream.Entries(); ok {
			t.Fatal("replay skipped over evicted history")
		}
		if !errors.Is(stream.Err(), ErrGapped) {
			t.Fatalf("replay error = %v, want missing history", stream.Err())
		}
	})
}

func TestSubscriptionNormalLogCloseDrainsOrderedTail(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log := New(Options{Capacity: 16, SubscriberBuffer: 16})
		stream, err := log.SubscribeStream(1, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		for i := 1; i <= 3; i++ {
			if _, err := log.Append(i, ts(int64(i))); err != nil {
				t.Fatal(err)
			}
		}
		synctest.Wait()
		if err := log.Close(); err != nil {
			t.Fatal(err)
		}
		count := 0
		for entry := range stream.Entries() {
			count++
			if entry.Seq != uint64(count) {
				t.Fatalf("normal close reordered tail: %+v", entry)
			}
		}
		if count != 3 || stream.Err() != nil {
			t.Fatalf("normal close: count=%d error=%v", count, stream.Err())
		}
	})
}
