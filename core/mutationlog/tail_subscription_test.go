package mutationlog

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"testing/synctest"
)

func TestSubscribeTailOwnsUpdatesAcrossRingEviction(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log := New(Options{Capacity: 4, SubscriberBuffer: 16})
		defer log.Close()
		for n := 1; n <= 8; n++ {
			if _, err := log.Append([]byte{byte(n)}, ts(int64(n))); err != nil {
				t.Fatal(err)
			}
		}
		projected := 0
		tail, err := log.SubscribeTail(9, 512, func(e Entry) (Entry, error) {
			projected++
			e.Op = append([]byte(nil), e.Op.([]byte)...)
			return e, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		defer tail.Close()
		originals := make([][]byte, 0, 8)
		for n := 9; n <= 16; n++ {
			value := []byte{byte(n)}
			originals = append(originals, value)
			if _, err := log.Append(value, ts(int64(n))); err != nil {
				t.Fatal(err)
			}
		}
		synctest.Wait()
		if first, _ := log.FirstSeq(); first != 13 {
			t.Fatalf("first retained seq = %d, want 13", first)
		}
		if projected != 8 {
			t.Fatalf("projected %d entries, want exactly the 8 post-cut updates", projected)
		}
		for _, original := range originals {
			original[0] = 0 // Only the owned projection may survive in the tail.
		}
		for n := uint64(9); n <= 16; n++ {
			entry := <-tail.Entries()
			if entry.Seq != n || entry.HLC != ts(int64(n)) || entry.Op.([]byte)[0] != byte(n) {
				t.Fatalf("tail seq %d = %+v", n, entry)
			}
		}
		if tail.Err() != nil {
			t.Fatalf("retained owned tail failed: %v", tail.Err())
		}
	})
}

func TestSubscribeTailRequiresExactNextSequence(t *testing.T) {
	log := New(Options{})
	defer log.Close()
	copyEntry := func(e Entry) (Entry, error) { return e, nil }
	if _, err := log.SubscribeTail(1, 512, nil); err == nil {
		t.Fatal("nil projector admitted")
	}
	for _, limit := range []int{0, -1} {
		if _, err := log.SubscribeTail(1, limit, copyEntry); err == nil {
			t.Fatalf("nonpositive maxEntries=%d admitted", limit)
		}
	}
	for _, from := range []uint64{0, 2} {
		if _, err := log.SubscribeTail(from, 512, copyEntry); !errors.Is(err, ErrGapped) {
			t.Fatalf("cold from=%d error = %v, want gap", from, err)
		}
	}
	tail, err := log.SubscribeTail(1, 512, copyEntry)
	if err != nil {
		t.Fatal(err)
	}
	_ = tail.Close()
	if _, err := log.Append("one", ts(1)); err != nil {
		t.Fatal(err)
	}
	for _, from := range []uint64{0, 1, 3} {
		if _, err := log.SubscribeTail(from, 512, copyEntry); !errors.Is(err, ErrGapped) {
			t.Fatalf("from=%d error = %v, want gap", from, err)
		}
	}
	tail, err = log.SubscribeTail(2, 512, copyEntry)
	if err != nil {
		t.Fatal(err)
	}
	_ = tail.Close()
	log.mu.Lock()
	log.lastSeq = math.MaxUint64
	log.mu.Unlock()
	if _, err := log.SubscribeTail(0, 512, copyEntry); !errors.Is(err, ErrGapped) {
		t.Fatalf("exhausted log accepted a wrapped tail: %v", err)
	}
}

func TestSubscribeTailEntryLimitBoundsQueueAllocation(t *testing.T) {
	for _, row := range []struct{ configured, limit, want int }{
		{configured: 4096, limit: 512, want: 512},
		{configured: 2, limit: 512, want: 2},
	} {
		log := New(Options{SubscriberBuffer: row.configured})
		tail, err := log.SubscribeTail(1, row.limit, func(e Entry) (Entry, error) { return e, nil })
		if err != nil {
			_ = log.Close()
			t.Fatal(err)
		}
		if got := cap(tail.entries); got != row.want {
			t.Errorf("configured=%d limit=%d: queue capacity=%d, want %d", row.configured, row.limit, got, row.want)
		}
		_ = tail.Close()
		_ = log.Close()
	}
}

func TestSubscribeTailProjectionFailureStopsBeforeLaterEntries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log := New(Options{SubscriberBuffer: 4})
		defer log.Close()
		rejected := errors.New("owned byte budget exhausted")
		projected := 0
		tail, err := log.SubscribeTail(1, 512, func(e Entry) (Entry, error) {
			projected++
			if e.Seq == 2 {
				return Entry{}, rejected
			}
			e.Op = []byte("owned")
			return e, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		for n := 1; n <= 3; n++ {
			if _, err := log.Append(n, ts(int64(n))); err != nil {
				t.Fatal(err)
			}
		}
		synctest.Wait()
		select {
		case <-tail.Done():
		default:
			t.Fatal("projection failure hidden behind queued entry")
		}
		if projected != 2 || !errors.Is(tail.Err(), rejected) {
			t.Fatalf("projection count=%d terminal=%v", projected, tail.Err())
		}
		if errors.Is(tail.Err(), ErrTailQueueFull) {
			t.Fatal("projector failure was mislabeled as queue overflow")
		}
		if len(tail.entries) != 1 {
			t.Fatalf("queued %d entries after projection failure, want 1", len(tail.entries))
		}
		for range 2 {
			if err := tail.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if _, open := <-tail.Entries(); open {
			t.Fatal("Close retained queued owned payload")
		}
	})
}

func TestSubscribeTailOverflowDoesNotConstructExtraPayload(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log := New(Options{Capacity: 1, SubscriberBuffer: 2})
		defer log.Close()
		projected := 0
		tail, err := log.SubscribeTail(1, 512, func(e Entry) (Entry, error) {
			projected++
			e.Op = []byte{byte(e.Seq)}
			return e, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		for n := 1; n <= 3; n++ {
			if _, err := log.Append(n, ts(int64(n))); err != nil {
				t.Fatal(err)
			}
		}
		synctest.Wait()
		if projected != 2 || !errors.Is(tail.Err(), ErrSubscriptionLost) || !errors.Is(tail.Err(), ErrTailQueueFull) {
			t.Fatalf("projection count=%d terminal=%v", projected, tail.Err())
		}
		select {
		case <-tail.Done():
		default:
			t.Fatal("overflow waited for buffered payloads to drain")
		}
		_ = tail.Close()
		if !errors.Is(tail.Err(), ErrTailQueueFull) {
			t.Fatal("owner cleanup replaced the first queue-overflow cause")
		}
		if len(tail.entries) != 0 {
			t.Fatal("Close retained overflowed payloads")
		}
	})
}

func TestSubscribeTailProjectorLossRemainsDistinctFromQueueOverflow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log := New(Options{SubscriberBuffer: 2})
		defer log.Close()
		tail, err := log.SubscribeTail(1, 512, func(Entry) (Entry, error) {
			return Entry{}, ErrSubscriptionLost
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := log.Append("rejected by projector", ts(1)); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if !errors.Is(tail.Err(), ErrSubscriptionLost) || errors.Is(tail.Err(), ErrTailQueueFull) {
			t.Fatalf("projector terminal classification = %v", tail.Err())
		}
		_ = tail.Close()
		if !errors.Is(tail.Err(), ErrSubscriptionLost) || errors.Is(tail.Err(), ErrTailQueueFull) {
			t.Fatal("owner cleanup changed the projector's first terminal cause")
		}
		if _, open := <-tail.Entries(); open {
			t.Fatal("failed projector retained a payload")
		}
	})
}

func TestSubscribeTailLogInvalidationSignalsWithoutReading(t *testing.T) {
	for _, cause := range []string{"close", "fault", "boundary"} {
		t.Run(cause, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				log := New(Options{SubscriberBuffer: 4})
				defer log.Close()
				tail, err := log.SubscribeTail(1, 512, func(e Entry) (Entry, error) {
					e.Op = []byte("owned")
					return e, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := log.Append(1, ts(1)); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				want := ErrClosed
				switch cause {
				case "close":
					if err := log.Close(); err != nil {
						t.Fatal(err)
					}
				case "fault":
					want = ErrWALIndeterminate
					log.mu.Lock()
					log.markUnusableLocked(want)
					log.mu.Unlock()
				case "boundary":
					want = ErrGapped
					log.mu.Lock()
					log.installBoundaryLocked(5)
					log.mu.Unlock()
				}
				select {
				case <-tail.Done():
				default:
					t.Fatal("log invalidation waited for unread payload")
				}
				if !errors.Is(tail.Err(), want) {
					t.Fatalf("terminal error = %v, want %v", tail.Err(), want)
				}
				_ = tail.Close()
				if _, open := <-tail.Entries(); open {
					t.Fatal("Close retained invalidated payload")
				}
			})
		})
	}
}

func TestSubscribeTailCloseJoinsProjection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log := New(Options{SubscriberBuffer: 4})
		defer log.Close()
		entered, release := make(chan struct{}), make(chan struct{})
		tail, err := log.SubscribeTail(1, 512, func(e Entry) (Entry, error) {
			close(entered)
			<-release
			e.Op = []byte("owned")
			return e, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := log.Append(1, ts(1)); err != nil {
			t.Fatal(err)
		}
		<-entered
		closed := make(chan struct{})
		go func() { _ = tail.Close(); close(closed) }()
		select {
		case <-closed:
			t.Fatal("Close returned while projection still owned its input")
		default:
		}
		close(release)
		<-closed
		if !errors.Is(tail.Err(), context.Canceled) {
			t.Fatalf("Close terminal error = %v", tail.Err())
		}
		if _, open := <-tail.Entries(); open {
			t.Fatal("payload enqueued during Close survived its drain")
		}
		var owners sync.WaitGroup
		for range 4 {
			owners.Go(func() { _ = tail.Close() })
		}
		owners.Wait()
	})
}
