package mutationlog

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
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

func TestCommitWithPostRingPublicationReleasesAfterRingBeforeDispatch(t *testing.T) {
	l := New(Options{Capacity: 2, SubscriberBuffer: 2})
	defer l.Close()
	ch, cancel, err := l.Subscribe(1)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	var external sync.RWMutex
	external.Lock() // Model a staged GraphCache or receipt Store transaction.
	callbackEntered := make(chan struct{})
	releaseCallback := make(chan struct{})
	defer func() {
		select {
		case <-releaseCallback:
		default:
			close(releaseCallback)
		}
	}()
	type result struct {
		entry Entry
		err   error
	}
	commitDone := make(chan result, 1)
	go func() {
		entry, err := l.CommitWithPostRingPublication("receipt", ts(1), func(entry Entry) {
			// The callback runs under Log.mu, so inspect its internal ring
			// rather than calling a public method that would deadlock.
			if !l.hasEntries || l.lastSeq != entry.Seq || l.size != 1 || l.ring[l.head].Seq != entry.Seq {
				t.Errorf("post-ring callback ran before ring/seq installation: entry=%+v, ring=%+v", entry, l.ring)
			}
			external.Unlock()
			close(callbackEntered)
			<-releaseCallback
		})
		commitDone <- result{entry, err}
	}()
	select {
	case <-callbackEntered:
	case <-time.After(time.Second):
		t.Fatal("post-ring callback was not entered")
	}
	// A raw external reader may now see the committed state. Public log
	// readers still wait for Log.mu, then must see that same installed entry.
	externalReadDone := make(chan struct{})
	go func() {
		external.RLock()
		external.RUnlock()
		close(externalReadDone)
	}()
	select {
	case <-externalReadDone:
	case <-time.After(time.Second):
		t.Fatal("post-ring callback did not release external readers")
	}
	logReadDone := make(chan uint64, 1)
	go func() {
		seq, _ := l.LastSeq()
		logReadDone <- seq
	}()
	select {
	case seq := <-logReadDone:
		t.Fatalf("log reader escaped callback: seq=%d", seq)
	case frame := <-ch:
		t.Fatalf("subscriber received entry before dispatch: %+v", frame)
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseCallback)
	select {
	case got := <-commitDone:
		if got.err != nil || got.entry.Seq != 1 {
			t.Fatalf("post-ring commit = %+v, want seq 1", got)
		}
	case <-time.After(time.Second):
		t.Fatal("post-ring commit did not finish")
	}
	if seq := <-logReadDone; seq != 1 {
		t.Fatalf("log reader after release = %d, want 1", seq)
	}
	select {
	case frame := <-ch:
		if frame.Seq != 1 {
			t.Fatalf("subscriber seq = %d, want 1", frame.Seq)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive committed entry")
	}
}

func TestCommitWithPostRingPublicationDefiniteAbortSkipsRelease(t *testing.T) {
	w := &scriptedWAL{fail: &DefiniteWALAbort{Cause: errors.New("before record")}}
	l := New(Options{WAL: w})
	defer l.Close()
	released := false
	if _, err := l.CommitWithPostRingPublication("receipt", ts(1), func(Entry) { released = true }); err == nil {
		t.Fatal("definite WAL abort was accepted")
	}
	if released {
		t.Fatal("post-ring release ran after definite WAL abort")
	}
	if _, ok := l.LastSeq(); ok {
		t.Fatal("definite WAL abort installed a ring entry")
	}
}

func TestCommitWithPostRingPublicationRawCoreReaders(t *testing.T) {
	cache := graphcache.NewGraphCacheWithStaging[string, string](time.Hour)
	cache.AddEdge("tail", "head", 1)
	epoch := mutationreceipt.Epoch{1}
	store, err := mutationreceipt.New(mutationreceipt.Config{
		Epoch: epoch, Retention: time.Hour, MaxEntries: 2, MaxBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	id, err := mutationreceipt.NewID(epoch, time.Now().Add(-time.Second), [24]byte{1})
	if err != nil {
		t.Fatal(err)
	}
	receiptTx, err := store.Begin(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer receiptTx.Abort()
	if _, _, err := receiptTx.Classify([]mutationreceipt.Intent{{
		ID: id, Group: mutationreceipt.GroupID{1}, Index: 0, Count: 1,
		Kind: mutationreceipt.DeleteEdge, Digest: [32]byte{1},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := receiptTx.Reserve([][]byte{{1}}); err != nil {
		t.Fatal(err)
	}
	if err := receiptTx.Stage(); err != nil {
		t.Fatal(err)
	}
	graphTx, err := cache.BeginEdgeDelete(
		[]graphcache.EdgeKey[string]{{Tail: "tail", Head: "head"}},
		ts(1), time.Now().Add(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer graphTx.Abort()
	l := New(Options{Capacity: 2})
	defer l.Close()
	entered := make(chan struct{})
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	commitDone := make(chan error, 1)
	go func() {
		_, err := l.CommitWithPostRingPublication("receipt", ts(1), func(entry Entry) {
			if l.lastSeq != entry.Seq || !l.hasEntries || l.ring[l.head].Seq != entry.Seq {
				t.Errorf("raw Core release preceded log-ring publication: entry=%+v", entry)
			}
			receiptTx.Commit()
			graphTx.Commit()
			close(entered)
			<-release
		})
		commitDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("post-ring raw Core release did not run")
	}
	storeRead := make(chan mutationreceipt.Status, 1)
	go func() {
		status, _, _ := store.Lookup(id, time.Now())
		storeRead <- status
	}()
	graphRead := make(chan bool, 1)
	go func() {
		_, live := cache.GetWeight("tail", "head")
		graphRead <- live
	}()
	snapshotRead := make(chan int, 1)
	go func() { snapshotRead <- len(cache.SnapshotEdges()) }()
	select {
	case status := <-storeRead:
		if status != mutationreceipt.Confirmed {
			t.Fatalf("Store.Lookup = %v, want Confirmed", status)
		}
	case <-time.After(time.Second):
		t.Fatal("Store.Lookup remained blocked after post-ring release")
	}
	select {
	case live := <-graphRead:
		if live {
			t.Fatal("GraphCache.GetWeight observed pre-Delete edge")
		}
	case <-time.After(time.Second):
		t.Fatal("GraphCache.GetWeight remained blocked after post-ring release")
	}
	select {
	case count := <-snapshotRead:
		if count != 0 {
			t.Fatalf("GraphCache.SnapshotEdges retained %d deleted edges", count)
		}
	case <-time.After(time.Second):
		t.Fatal("GraphCache.SnapshotEdges remained blocked after post-ring release")
	}
	logRead := make(chan uint64, 1)
	go func() {
		seq, _ := l.LastSeq()
		logRead <- seq
	}()
	select {
	case seq := <-logRead:
		t.Fatalf("Log.LastSeq returned %d before callback completed", seq)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-commitDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("post-ring commit did not finish")
	}
	select {
	case seq := <-logRead:
		if seq != 1 {
			t.Fatalf("Log.LastSeq after Core release = %d, want 1", seq)
		}
	case <-time.After(time.Second):
		t.Fatal("Log.LastSeq did not finish")
	}
}

func TestCommitWithPostRingPublicationReleasesBeforeDispatchBackpressure(t *testing.T) {
	var external sync.RWMutex
	external.Lock()
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(external.Unlock) }
	dropEntered := make(chan struct{})
	l := New(Options{
		Capacity: 8, SubscriberBuffer: 1,
		OnDrop: func(string) {
			close(dropEntered)
			external.RLock()
			external.RUnlock()
		},
	})
	defer l.Close()
	defer release()
	// Register a raw live subscriber so the forwarder cannot drain its
	// one-entry buffer into a caller-facing channel during this test.
	live := &subscription{ch: make(chan Entry, 1), startSeq: 1}
	l.subsMu.Lock()
	l.subscribers[live] = struct{}{}
	l.subsMu.Unlock()
	if _, err := l.Append("fill subscriber", ts(1)); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	for len(live.ch) == 0 {
		select {
		case <-deadline:
			t.Fatal("subscriber was not filled")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if _, err := l.Append("drop subscriber", ts(2)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-dropEntered:
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not reach blocked OnDrop")
	}
	// The dispatcher is blocked on the staged external lock. One more entry
	// fills its inbox; the receipt commit would deadlock at dispatch handoff
	// if it waited to release that lock until after the send.
	if _, err := l.Append("fill dispatcher inbox", ts(3)); err != nil {
		t.Fatal(err)
	}
	commitDone := make(chan error, 1)
	go func() {
		_, err := l.CommitWithPostRingPublication("receipt", ts(4), func(Entry) { release() })
		commitDone <- err
	}()
	select {
	case err := <-commitDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("receipt commit deadlocked behind dispatcher backpressure")
	}
}

func TestCommitWithPostRingPublicationPanicPoisonsLog(t *testing.T) {
	l := New(Options{Capacity: 2})
	defer l.Close()
	func() {
		defer func() {
			if recover() == nil {
				t.Error("post-ring panic did not propagate")
			}
		}()
		_, _ = l.CommitWithPostRingPublication("receipt", ts(1), func(Entry) { panic("release interrupted") })
	}()
	if seq, ok := l.LastSeq(); !ok || seq != 1 {
		t.Fatalf("post-ring panic seq = %d/%v, want installed seq 1", seq, ok)
	}
	if _, err := l.Append("later", ts(2)); !errors.Is(err, ErrPublicationInterrupted) {
		t.Fatalf("write after interrupted release = %v, want ErrPublicationInterrupted", err)
	}
	if _, _, err := l.Subscribe(1); !errors.Is(err, ErrPublicationInterrupted) {
		t.Fatalf("Subscribe after interrupted release = %v, want ErrPublicationInterrupted", err)
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
