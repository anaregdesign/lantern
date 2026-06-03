package mutationlog

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

func ts(n int64) hlc.Timestamp {
	return hlc.Timestamp{WallNs: n}
}

func TestAppendAssignsMonotonicSeq(t *testing.T) {
	l := New(Options{Capacity: 4})
	for i := 1; i <= 5; i++ {
		e, err := l.Append(i, ts(int64(i)))
		if err != nil {
			t.Fatalf("append #%d: %v", i, err)
		}
		if got, want := e.Seq, uint64(i); got != want {
			t.Fatalf("seq = %d, want %d", got, want)
		}
	}
	last, ok := l.LastSeq()
	if !ok || last != 5 {
		t.Fatalf("LastSeq = (%d, %v), want (5, true)", last, ok)
	}
	// Capacity 4, so first seq should now be 2 (oldest evicted).
	first, ok := l.FirstSeq()
	if !ok || first != 2 {
		t.Fatalf("FirstSeq = (%d, %v), want (2, true)", first, ok)
	}
}

func TestSubscribeReplaysAndStreams(t *testing.T) {
	l := New(Options{Capacity: 16, SubscriberBuffer: 16})
	for i := 1; i <= 3; i++ {
		if _, err := l.Append(i, ts(int64(i))); err != nil {
			t.Fatal(err)
		}
	}
	ch, cancel, err := l.Subscribe(1)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cancel()
	// Replay
	for i := 1; i <= 3; i++ {
		select {
		case e := <-ch:
			if e.Seq != uint64(i) {
				t.Fatalf("replay seq = %d, want %d", e.Seq, i)
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout on replay #%d", i)
		}
	}
	// Live
	if _, err := l.Append(4, ts(4)); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-ch:
		if e.Seq != 4 || e.Op.(int) != 4 {
			t.Fatalf("live = %+v, want seq=4 op=4", e)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout on live")
	}
}

func TestSubscribeRequestsBelowFirstSeqReportsGap(t *testing.T) {
	l := New(Options{Capacity: 2})
	for i := 1; i <= 5; i++ {
		if _, err := l.Append(i, ts(int64(i))); err != nil {
			t.Fatal(err)
		}
	}
	first, _ := l.FirstSeq()
	if first != 4 { // ring keeps 4,5
		t.Fatalf("FirstSeq = %d, want 4", first)
	}
	if _, _, err := l.Subscribe(1); !errors.Is(err, ErrGapped) {
		t.Fatalf("err = %v, want ErrGapped", err)
	}
}

func TestSubscribeFromFutureSeqStreamsLiveOnly(t *testing.T) {
	l := New(Options{Capacity: 8, SubscriberBuffer: 4})
	for i := 1; i <= 3; i++ {
		if _, err := l.Append(i, ts(int64(i))); err != nil {
			t.Fatal(err)
		}
	}
	ch, cancel, err := l.Subscribe(10) // beyond last
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cancel()
	if _, err := l.Append(4, ts(4)); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-ch:
		if e.Seq != 4 {
			t.Fatalf("got seq %d, want 4", e.Seq)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestFanoutToManySubscribers(t *testing.T) {
	const n = 8
	l := New(Options{Capacity: 32, SubscriberBuffer: 32})
	chs := make([]<-chan Entry, n)
	cancels := make([]func() error, n)
	for i := 0; i < n; i++ {
		ch, cancel, err := l.Subscribe(1)
		if err != nil {
			t.Fatal(err)
		}
		chs[i] = ch
		cancels[i] = cancel
	}
	defer func() {
		for _, c := range cancels {
			c()
		}
	}()
	for i := 1; i <= 5; i++ {
		if _, err := l.Append(i, ts(int64(i))); err != nil {
			t.Fatal(err)
		}
	}
	for i, ch := range chs {
		for j := 1; j <= 5; j++ {
			select {
			case e := <-ch:
				if e.Seq != uint64(j) {
					t.Fatalf("sub %d seq = %d, want %d", i, e.Seq, j)
				}
			case <-time.After(time.Second):
				t.Fatalf("sub %d timeout at j=%d", i, j)
			}
		}
	}
}

func TestSlowSubscriberIsDroppedNotBlocking(t *testing.T) {
	// Tiny buffer so a single un-drained extra entry forces a drop.
	l := New(Options{Capacity: 64, SubscriberBuffer: 2})
	slow, slowCancel, err := l.Subscribe(1)
	if err != nil {
		t.Fatal(err)
	}
	defer slowCancel()

	// Append more than SubscriberBuffer entries without draining slow. The
	// fan-out must not block — otherwise Append would never return.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 1; i <= 20; i++ {
			if _, err := l.Append(i, ts(int64(i))); err != nil {
				t.Errorf("append: %v", err)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Append blocked on slow subscriber — back-pressure broken")
	}

	// slow must be closed (drop-then-close protocol). Drain whatever made
	// it in (up to SubscriberBuffer entries) until we observe close.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-slow:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("slow subscriber was never closed")
		}
	}
}

func TestConcurrentAppendsAreSerialised(t *testing.T) {
	const writers = 8
	const perWriter = 250
	l := New(Options{Capacity: writers * perWriter, SubscriberBuffer: 1024})
	var wg sync.WaitGroup
	wg.Add(writers)
	for w := 0; w < writers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				if _, err := l.Append(w*1000+i, ts(int64(w*1000+i))); err != nil {
					t.Errorf("append: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	last, _ := l.LastSeq()
	if last != writers*perWriter {
		t.Fatalf("LastSeq = %d, want %d", last, writers*perWriter)
	}
	first, _ := l.FirstSeq()
	if first != 1 {
		t.Fatalf("FirstSeq = %d, want 1", first)
	}
}

type recordingWAL struct {
	mu      sync.Mutex
	entries []Entry
	fail    error
}

func (w *recordingWAL) Write(e Entry) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fail != nil {
		return w.fail
	}
	w.entries = append(w.entries, e)
	return nil
}

func TestWALReceivesEntriesInOrder(t *testing.T) {
	w := &recordingWAL{}
	l := New(Options{Capacity: 8, WAL: w})
	for i := 1; i <= 3; i++ {
		if _, err := l.Append(i, ts(int64(i))); err != nil {
			t.Fatal(err)
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.entries) != 3 {
		t.Fatalf("wal len = %d, want 3", len(w.entries))
	}
	for i, e := range w.entries {
		if e.Seq != uint64(i+1) {
			t.Fatalf("wal[%d].Seq = %d, want %d", i, e.Seq, i+1)
		}
	}
}

func TestWALErrorAbortsAppend(t *testing.T) {
	want := errors.New("disk full")
	w := &recordingWAL{fail: want}
	l := New(Options{Capacity: 8, WAL: w})
	if _, err := l.Append(1, ts(1)); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if _, ok := l.LastSeq(); ok {
		t.Fatal("log should remain empty after WAL failure")
	}
}

func TestCloseRejectsFurtherAppends(t *testing.T) {
	l := New(Options{Capacity: 4})
	if _, err := l.Append(1, ts(1)); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(2, ts(2)); !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
	if _, _, err := l.Subscribe(1); !errors.Is(err, ErrClosed) {
		t.Fatalf("subscribe err = %v, want ErrClosed", err)
	}
}

func TestCloseClosesLiveSubscribers(t *testing.T) {
	l := New(Options{Capacity: 4, SubscriberBuffer: 4})
	ch, cancel, err := l.Subscribe(0)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected closed channel after Close")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for channel close")
	}
}

func TestCancelIsIdempotent(t *testing.T) {
	l := New(Options{Capacity: 4})
	_, cancel, err := l.Subscribe(0)
	if err != nil {
		t.Fatal(err)
	}
	if err := cancel(); err != nil {
		t.Fatal(err)
	}
	if err := cancel(); err != nil {
		t.Fatalf("second cancel = %v, want nil", err)
	}
}
