package mutationlog

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"testing/synctest"
)

func replayTailOverflowFixture(t *testing.T, capacity, buffer int) (*Log, *Subscription) {
	t.Helper()
	log := New(Options{Capacity: capacity, SubscriberBuffer: buffer})
	old, err := log.SubscribeTail(1, 512, func(entry Entry) (Entry, error) {
		entry.Op = append([]byte(nil), entry.Op.([]byte)...)
		return entry, nil
	})
	if err != nil {
		_ = log.Close()
		t.Fatal(err)
	}
	for n := 1; n <= buffer+1; n++ {
		if _, err := log.Append([]byte{byte(n)}, ts(int64(n))); err != nil {
			_ = old.Close()
			_ = log.Close()
			t.Fatal(err)
		}
	}
	synctest.Wait()
	if !errors.Is(old.Err(), ErrTailQueueFull) {
		_ = old.Close()
		_ = log.Close()
		t.Fatal("fixture did not overflow its original owned queue")
	}
	_ = old.Close() // Join/drain precedes every replacement and credit refund.
	return log, old
}

func TestReplayTailStreams513RetainedEntriesWithoutCopyingReplayWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log, old := replayTailOverflowFixture(t, 1024, 512)
		defer log.Close()
		if err := old.ValidateReplay(1); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		projected := 0
		replay, err := old.ReplayTail(1, 512, func(entry Entry) (Entry, error) {
			projected++
			entry.Op = append([]byte(nil), entry.Op.([]byte)...)
			return entry, nil
		}, start)
		if err != nil {
			t.Fatal(err)
		}
		defer replay.Close()
		synctest.Wait()
		if projected != 0 || cap(replay.entries) != 0 {
			t.Fatal("paused replay projected data or allocated a replay window output")
		}
		close(start)
		synctest.Wait()
		if projected != 1 {
			t.Fatalf("unread replay projected %d entries, want only one owned holder", projected)
		}
		log.mu.Lock()
		log.ring[log.head].Op.([]byte)[0] = 0 // The first holder must already own its bytes.
		log.mu.Unlock()
		for n := uint64(1); n <= 514; n++ {
			if n == 2 {
				// Arrives during replay; the atomic live switch must neither lose
				// it nor duplicate it through the dispatcher's queued delivery.
				if _, err := log.Append([]byte{byte(514 % 256)}, ts(514)); err != nil {
					t.Fatal(err)
				}
			}
			entry, open := <-replay.Entries()
			if !open || entry.Seq != n || entry.HLC != ts(int64(n)) || entry.Op.([]byte)[0] != byte(n) {
				t.Fatalf("owned replay position %d = %+v open=%t", n, entry, open)
			}
		}
		synctest.Wait()
		log.subsMu.Lock()
		live := false
		for sub := range log.subscribers {
			if sub.stream == replay {
				live = !sub.replaying && sub.startSeq == 515
			}
		}
		log.subsMu.Unlock()
		if !live {
			t.Fatal("replay did not atomically catch up and register the next live position")
		}
		if _, err := log.Append([]byte{byte(515 % 256)}, ts(515)); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		entry := <-replay.Entries()
		if entry.Seq != 515 || entry.Op.([]byte)[0] != byte(515%256) || projected != 515 || replay.Err() != nil {
			t.Fatalf("live successor = %+v projections=%d terminal=%v", entry, projected, replay.Err())
		}
	})
}

func TestReplayTailCloseBeforeStartJoinsWithoutProjecting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log, old := replayTailOverflowFixture(t, 8, 2)
		defer log.Close()
		start := make(chan struct{})
		projected := 0
		replay, err := old.ReplayTail(1, 512, func(entry Entry) (Entry, error) {
			projected++
			entry.Op = []byte("owned")
			return entry, nil
		}, start)
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		var owners sync.WaitGroup
		for range 4 {
			owners.Go(func() { _ = replay.Close() })
		}
		owners.Wait()
		close(start)
		synctest.Wait()
		if projected != 0 || !errors.Is(replay.Err(), context.Canceled) {
			t.Fatalf("closed paused replay projected=%d terminal=%v", projected, replay.Err())
		}
		if _, open := <-replay.Entries(); open {
			t.Fatal("closed paused replay retained output")
		}
		if !errors.Is(old.Err(), ErrTailQueueFull) || old.ValidateReplay(1) != nil {
			t.Fatal("replacement cleanup changed the original queue-full evidence")
		}
	})
}

func TestReplayTailPausedInvalidationAndGenerationBinding(t *testing.T) {
	for _, cause := range []string{"close", "fault", "boundary", "generation"} {
		t.Run(cause, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				log, old := replayTailOverflowFixture(t, 8, 2)
				defer log.Close()
				start := make(chan struct{})
				projected := 0
				replay, err := old.ReplayTail(1, 512, func(entry Entry) (Entry, error) {
					projected++
					entry.Op = []byte("owned")
					return entry, nil
				}, start)
				if err != nil {
					t.Fatal(err)
				}
				want := ErrGapped
				switch cause {
				case "close":
					want = ErrClosed
					_ = log.Close()
				case "fault":
					want = ErrWALIndeterminate
					log.mu.Lock()
					log.markUnusableLocked(want)
					log.mu.Unlock()
				case "boundary":
					log.mu.Lock()
					log.installBoundaryLocked(4)
					log.mu.Unlock()
				case "generation":
					log.mu.Lock()
					log.tailGeneration = new(byte) // Same retained range; independent generation proof.
					log.mu.Unlock()
				}
				if err := old.ValidateReplay(1); !errors.Is(err, want) {
					t.Fatalf("original replay validation = %v, want %v", err, want)
				}
				close(start)
				synctest.Wait()
				if !errors.Is(replay.Err(), want) || projected != 0 {
					t.Fatalf("invalidated paused replay projected=%d terminal=%v, want %v", projected, replay.Err(), want)
				}
				_ = replay.Close()
				if _, open := <-replay.Entries(); open {
					t.Fatal("invalidated replay retained output")
				}
				if reopened, err := old.ReplayTail(1, 512, func(entry Entry) (Entry, error) { return entry, nil }, start); !errors.Is(err, want) || reopened != nil {
					if reopened != nil {
						_ = reopened.Close()
					}
					t.Fatalf("old-generation replacement reopened: err=%v", err)
				}
			})
		})
	}
}

func TestReplayTailEvictionDuringSlowReplayIsTerminal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log, old := replayTailOverflowFixture(t, 4, 2)
		defer log.Close()
		start := make(chan struct{})
		projected := 0
		replay, err := old.ReplayTail(1, 512, func(entry Entry) (Entry, error) {
			projected++
			entry.Op = append([]byte(nil), entry.Op.([]byte)...)
			return entry, nil
		}, start)
		if err != nil {
			t.Fatal(err)
		}
		defer replay.Close()
		close(start)
		synctest.Wait()
		for n := 4; n <= 8; n++ {
			if _, err := log.Append([]byte{byte(n)}, ts(int64(n))); err != nil {
				t.Fatal(err)
			}
		}
		entry := <-replay.Entries()
		if entry.Seq != 1 || entry.Op.([]byte)[0] != 1 {
			t.Fatal("already projected owned holder changed after ring eviction")
		}
		synctest.Wait()
		if !errors.Is(replay.Err(), ErrGapped) || projected != 1 {
			t.Fatalf("replay skipped evicted history: projections=%d terminal=%v", projected, replay.Err())
		}
		if _, open := <-replay.Entries(); open {
			t.Fatal("replay delivered a later entry across an evicted prefix")
		}
		if err := old.ValidateReplay(1); !errors.Is(err, ErrGapped) {
			t.Fatalf("validation pinned history: %v", err)
		}
	})
}

func TestReplayTailProjectorFailureCannotRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log, old := replayTailOverflowFixture(t, 8, 2)
		defer log.Close()
		start := make(chan struct{})
		projected := 0
		replay, err := old.ReplayTail(1, 512, func(entry Entry) (Entry, error) {
			projected++
			if entry.Seq == 2 {
				return Entry{}, ErrSubscriptionLost // Projector budget, not queue overflow.
			}
			entry.Op = []byte("owned")
			return entry, nil
		}, start)
		if err != nil {
			t.Fatal(err)
		}
		close(start)
		if entry := <-replay.Entries(); entry.Seq != 1 {
			t.Fatalf("first replay position = %d", entry.Seq)
		}
		synctest.Wait()
		if projected != 2 || !errors.Is(replay.Err(), ErrSubscriptionLost) || errors.Is(replay.Err(), ErrTailQueueFull) {
			t.Fatalf("projector failure = %v projections=%d", replay.Err(), projected)
		}
		if err := replay.ValidateReplay(1); err == nil {
			t.Fatal("projector rejection became eligible for queue-loss replay")
		}
		_ = replay.Close()
		if !errors.Is(replay.Err(), ErrSubscriptionLost) || errors.Is(replay.Err(), ErrTailQueueFull) {
			t.Fatal("Close replaced projector terminal evidence")
		}
	})
}

func TestReplayTailCloseJoinsSingleProjectedHolder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log, old := replayTailOverflowFixture(t, 8, 2)
		defer log.Close()
		start, entered, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
		replay, err := old.ReplayTail(1, 512, func(entry Entry) (Entry, error) {
			close(entered)
			<-release
			entry.Op = []byte("owned")
			return entry, nil
		}, start)
		if err != nil {
			t.Fatal(err)
		}
		close(start)
		<-entered
		closed := make(chan struct{})
		go func() { _ = replay.Close(); close(closed) }()
		synctest.Wait()
		select {
		case <-closed:
			t.Fatal("Close returned before the replay projector relinquished its borrowed entry")
		default:
		}
		close(release)
		<-closed
		if !errors.Is(replay.Err(), context.Canceled) {
			t.Fatalf("Close terminal = %v", replay.Err())
		}
		if _, open := <-replay.Entries(); open {
			t.Fatal("Close retained replay output")
		}
	})
}

func TestReplayTailRejectsWrongCutIneligibleLossAndSequenceExhaustion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log, old := replayTailOverflowFixture(t, 8, 2)
		defer log.Close()
		project := func(entry Entry) (Entry, error) { entry.Op = []byte("owned"); return entry, nil }
		start := make(chan struct{})
		for _, from := range []uint64{0, 2, 5} {
			if err := old.ValidateReplay(from); !errors.Is(err, ErrGapped) {
				t.Fatalf("different original cut %d admitted: %v", from, err)
			}
		}
		for _, row := range []struct {
			limit   int
			project func(Entry) (Entry, error)
			start   <-chan struct{}
		}{{0, project, start}, {512, nil, start}, {512, project, nil}} {
			if replay, err := old.ReplayTail(1, row.limit, row.project, row.start); err == nil || replay != nil {
				if replay != nil {
					_ = replay.Close()
				}
				t.Fatal("invalid paused-replay configuration admitted")
			}
		}
		active, err := log.SubscribeTail(4, 512, project)
		if err != nil {
			t.Fatal(err)
		}
		if err := active.ValidateReplay(4); err == nil {
			t.Fatal("active tail admitted replay")
		}
		_ = active.Close()
		if err := active.ValidateReplay(4); err == nil {
			t.Fatal("explicitly canceled tail admitted replay")
		}
		log.mu.Lock()
		log.lastSeq = math.MaxUint64
		log.mu.Unlock()
		if err := old.ValidateReplay(1); !errors.Is(err, ErrSeqExhausted) {
			t.Fatalf("exhausted log replay validation = %v", err)
		}
		if replay, err := old.ReplayTail(1, 512, project, start); !errors.Is(err, ErrSeqExhausted) || replay != nil {
			if replay != nil {
				_ = replay.Close()
			}
			t.Fatalf("exhausted log replay opened: %v", err)
		}
	})
}
