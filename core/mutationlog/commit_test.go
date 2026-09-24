package mutationlog

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"
)

type scriptedWAL struct {
	mu      sync.Mutex
	entries []Entry
	fail    error
	onWrite func(Entry)
}

func (w *scriptedWAL) Write(e Entry) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.onWrite != nil {
		w.onWrite(e)
	}
	if w.fail != nil {
		err := w.fail
		w.fail = nil
		return err
	}
	w.entries = append(w.entries, e)
	return nil
}

func TestCommitWithPublicationWALFirstAndNoEarlyVisibility(t *testing.T) {
	var events []string
	w := &scriptedWAL{onWrite: func(Entry) { events = append(events, "wal") }}
	l := New(Options{Capacity: 4, SubscriberBuffer: 4, WAL: w})
	defer l.Close()
	ch, cancel, err := l.Subscribe(1)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	inPublish := make(chan struct{})
	release := make(chan struct{})
	type result struct {
		entry Entry
		err   error
	}
	committed := make(chan result, 1)
	go func() {
		e, err := l.CommitWithPublication("first", ts(1), func(e Entry) {
			if e.Seq != 1 {
				t.Errorf("publish seq = %d, want 1", e.Seq)
			}
			events = append(events, "publish")
			close(inPublish)
			<-release
		})
		committed <- result{e, err}
	}()
	select {
	case <-inPublish:
	case <-time.After(time.Second):
		t.Fatal("WAL or publication did not start")
	}
	// While publish is paused, neither a status reader, a second writer,
	// nor a subscriber may observe the pending sequence.
	readDone := make(chan uint64, 1)
	go func() {
		seq, _ := l.LastSeq()
		readDone <- seq
	}()
	appendDone := make(chan result, 1)
	go func() {
		e, err := l.Append("second", ts(2))
		appendDone <- result{e, err}
	}()
	select {
	case got := <-readDone:
		t.Fatalf("LastSeq returned %d before publication finished", got)
	case got := <-appendDone:
		t.Fatalf("Append returned %+v before publication finished", got)
	case got := <-ch:
		t.Fatalf("Subscribe observed %+v before publication finished", got)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if got := <-committed; got.err != nil || got.entry.Seq != 1 {
		t.Fatalf("commit = %+v, want seq 1", got)
	}
	if got := <-appendDone; got.err != nil || got.entry.Seq != 2 {
		t.Fatalf("subsequent append = %+v, want seq 2", got)
	}
	if got := <-readDone; got < 1 || got > 2 {
		t.Fatalf("LastSeq after release = %d, want 1 or 2", got)
	}
	for want := uint64(1); want <= 2; want++ {
		select {
		case got := <-ch:
			if got.Seq != want {
				t.Fatalf("Subscribe seq = %d, want %d", got.Seq, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("Subscribe timed out waiting for seq %d", want)
		}
	}
	if len(events) != 3 || events[0] != "wal" || events[1] != "publish" || events[2] != "wal" {
		t.Fatalf("events = %v, want WAL, publish, WAL", events)
	}
}

func TestCommitWithPublicationDefiniteAbortReusesSeq(t *testing.T) {
	diskFull := errors.New("disk full before record")
	w := &scriptedWAL{fail: &DefiniteWALAbort{Cause: diskFull}}
	l := New(Options{Capacity: 4, WAL: w})
	defer l.Close()
	published := false
	if _, err := l.CommitWithPublication("first", ts(1), func(Entry) { published = true }); !errors.Is(err, diskFull) {
		t.Fatalf("err = %v, want definite abort with original cause", err)
	}
	if published {
		t.Fatal("callback ran after definitely aborted WAL write")
	}
	if _, ok := l.LastSeq(); ok {
		t.Fatal("definitely aborted write advanced LastSeq")
	}
	e, err := l.CommitWithPublication("second", ts(2), func(Entry) { published = true })
	if err != nil || e.Seq != 1 || !published {
		t.Fatalf("next commit = (%+v, %v), published=%v; want seq 1", e, err, published)
	}
}

func TestLegacyDefiniteAbortAllowsReceiptCommit(t *testing.T) {
	diskFull := errors.New("disk full before record")
	w := &scriptedWAL{fail: &DefiniteWALAbort{Cause: diskFull}}
	l := New(Options{Capacity: 4, WAL: w})
	defer l.Close()
	if _, err := l.Append("legacy", ts(1)); !errors.Is(err, diskFull) {
		t.Fatalf("Append err = %v, want definite abort cause", err)
	}
	e, err := l.CommitWithPublication("receipt", ts(2), nil)
	if err != nil || e.Seq != 1 {
		t.Fatalf("receipt commit = (%+v, %v), want seq 1", e, err)
	}
}

func TestCommitWithPublicationIndeterminateWALFailsClosed(t *testing.T) {
	lostAck := errors.New("lost WAL acknowledgement")
	w := &scriptedWAL{fail: lostAck}
	// Simulate a WAL that persists the entry but loses its acknowledgement.
	w.onWrite = func(e Entry) { w.entries = append(w.entries, e) }
	l := New(Options{Capacity: 4, WAL: w})
	defer l.Close()
	ch, cancel, err := l.Subscribe(1)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	published := false
	if _, err := l.CommitWithPublication("first", ts(1), func(Entry) { published = true }); !errors.Is(err, ErrWALIndeterminate) || !errors.Is(err, lostAck) {
		t.Fatalf("err = %v, want indeterminate + original cause", err)
	}
	if published {
		t.Fatal("callback ran after failed WAL acknowledgement")
	}
	if _, ok := l.LastSeq(); ok {
		t.Fatal("indeterminate write advanced in-memory LastSeq")
	}
	if _, err := l.Append("second", ts(2)); !errors.Is(err, ErrWALIndeterminate) {
		t.Fatalf("Append err = %v, want ErrWALIndeterminate", err)
	}
	if _, err := l.CommitWithPublication("third", ts(3), nil); !errors.Is(err, ErrWALIndeterminate) {
		t.Fatalf("Commit err = %v, want ErrWALIndeterminate", err)
	}
	if _, _, err := l.Subscribe(1); !errors.Is(err, ErrWALIndeterminate) {
		t.Fatalf("Subscribe err = %v, want ErrWALIndeterminate", err)
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("old subscriber received indeterminate entry")
		}
	case <-time.After(time.Second):
		t.Fatal("old subscriber was not closed on indeterminate WAL")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.entries) != 1 || w.entries[0].Seq != 1 {
		t.Fatalf("WAL entries = %+v, want only potentially committed seq 1", w.entries)
	}
}

func TestCommitWithPublicationPanicPoisonsLog(t *testing.T) {
	l := New(Options{Capacity: 4})
	defer l.Close()
	func() {
		defer func() {
			if recover() == nil {
				t.Error("publish panic did not propagate")
			}
		}()
		_, _ = l.CommitWithPublication("first", ts(1), func(Entry) { panic("publication failed") })
	}()
	if _, ok := l.LastSeq(); ok {
		t.Fatal("panic advanced LastSeq")
	}
	if _, err := l.Append("second", ts(2)); !errors.Is(err, ErrPublicationInterrupted) {
		t.Fatalf("Append err = %v after panic, want ErrPublicationInterrupted", err)
	}
}

func TestCommitWithPublicationWALPanicPoisonsLog(t *testing.T) {
	w := &scriptedWAL{}
	w.onWrite = func(e Entry) {
		w.entries = append(w.entries, e)
		panic("WAL panic after record")
	}
	l := New(Options{Capacity: 4, WAL: w})
	defer l.Close()
	func() {
		defer func() {
			if recover() == nil {
				t.Error("WAL panic did not propagate")
			}
		}()
		_, _ = l.CommitWithPublication("first", ts(1), nil)
	}()
	if _, ok := l.LastSeq(); ok {
		t.Fatal("WAL panic advanced in-memory LastSeq")
	}
	if _, err := l.Append("second", ts(2)); !errors.Is(err, ErrWALIndeterminate) {
		t.Fatalf("Append err = %v after WAL panic, want ErrWALIndeterminate", err)
	}
}

func TestSeqExhaustionPrecedesWALAndPublication(t *testing.T) {
	w := &scriptedWAL{}
	l := New(Options{Capacity: 4, WAL: w})
	defer l.Close()
	l.lastSeq = math.MaxUint64
	l.hasEntries = true
	published := false
	if _, err := l.CommitWithPublication("next", ts(1), func(Entry) { published = true }); !errors.Is(err, ErrSeqExhausted) {
		t.Fatalf("Commit err = %v, want ErrSeqExhausted", err)
	}
	if _, err := l.Append("next", ts(1)); !errors.Is(err, ErrSeqExhausted) {
		t.Fatalf("Append err = %v, want ErrSeqExhausted", err)
	}
	if published || len(w.entries) != 0 {
		t.Fatalf("overflow wrote WAL or published: published=%v, entries=%+v", published, w.entries)
	}
}

// BenchmarkCommitWithPublication captures the additional callback boundary
// over the same bounded ring and dispatcher path as BenchmarkAppend.
func BenchmarkCommitWithPublication(b *testing.B) {
	l := New(Options{Capacity: 4096, SubscriberBuffer: 4096})
	defer l.Close()
	var published uint64
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := l.CommitWithPublication(i, ts(int64(i)), func(Entry) { published++ }); err != nil {
			b.Fatal(err)
		}
	}
	if published != uint64(b.N) {
		b.Fatalf("published = %d, want %d", published, b.N)
	}
}
